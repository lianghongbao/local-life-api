package user

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"local-life-api/internal/background"
	"local-life-api/internal/errorx"
	"local-life-api/internal/keys"
	"local-life-api/internal/search"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type PostListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// postListRow post_list_logic 的 DB 扫描目标(本地副本,不要复用 my_posts_logic 的 postRow)
//   字段数 = SELECT 列数。go-zero sqlx 默认 strict mode,字段数 > 列数会报 ErrNotMatchDestination
//   ("not matching destination to scan") — 详见 TestPostListRow_字段数匹配SELECT。
//   view_count 不在此 SELECT 中(改读 Redis incr),所以这里没有 ViewCount 字段。
//   把区划四字段(area_adcode / area_name / street_name / city_adcode)也透出,
//   C 端后续可能拿 adcode 做精确区过滤或展示"附近/同城"区名。
//   SearchScore 是搜索相关度分(仅搜索模式下 SELECT 才带上该列),非搜索模式为 0。
type postListRow struct {
	Id         uint64         `db:"id"`
	UserId     uint64         `db:"user_id"`
	// Nickname / Avatar 走 LEFT JOIN,发帖用户被注销时为 NULL,必须用 NullString 兼容
	//   否则报 "converting NULL to string is unsupported"(与 home_feed_logic 同款处理)
	Nickname   sql.NullString `db:"nickname"`
	Avatar     sql.NullString `db:"avatar"`
	Type       int64          `db:"type"`
	Title      string         `db:"title"`
	Images     sql.NullString `db:"images"`
	Content    sql.NullString `db:"content"`
	Area       string         `db:"area"`
	Latitude   float64        `db:"latitude"`
	Longitude  float64        `db:"longitude"`
	Contact    string         `db:"contact"`
	Status     int64          `db:"status"`
	CreatedAt  string         `db:"created_at"`
	AreaAdcode string         `db:"area_adcode"`
	AreaName   string         `db:"area_name"`
	StreetName string         `db:"street_name"`
	CityAdcode string         `db:"city_adcode"`
	SearchScore float64       `db:"search_score"`
}

func NewPostListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PostListLogic {
	return &PostListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// postListCacheTTLSeconds PostList 公开列表缓存时间（秒）
//   - 10s 短期缓存，吸收首页/列表页的重复查询
//   - DML（PostCreate/PostAudit/PostOffline）后最多 10s 自动失效
const postListCacheTTLSeconds = 10

// postListCacheKey 缓存 key：posts:type{type}:area{adcode}:kw{kwHash}:p{page}:s{size}
//   - type=0 表示"全部类型"，仍然作为 key 的一部分（独立 namespace）
//   - adcode="" 表示"全市"，仍然作为 key 的一部分（独立 namespace）
//   - kwHash=关键词哈希(search.CacheKeyHash)，空关键词时为 "0"
//     必须参与 key：否则搜"二手"会命中搜"租房"的缓存
func postListCacheKey(postType int, adcode string, kwHash string, page, pageSize int) string {
	if kwHash == "" {
		kwHash = "0"
	}
	return keys.Key(fmt.Sprintf("posts:type%d:area%s:kw%s:p%d:s%d", postType, adcode, kwHash, page, pageSize))
}

// PostList C 端公开信息列表
//   - 公开接口，仅展示 status=2（已通过审核）
//   - type=0 表示全部类型，否则按 type 精确过滤
//   - 合法 type: 1二手 2租房 3招聘 4求助 9其他（非法值兜底为"全部"）
//   - adcode 可选：空=全市；非空=按区过滤
//   - keyword 可选：非空时按 MySQL FULLTEXT(ngram) 搜标题+正文，
//     并改用相关度排序（MATCH score DESC, created_at DESC）
//   - 无 keyword 时按 created_at DESC 排序
//   - Redis 缓存 10s（DML 后自然失效；按 adcode/keyword 分桶避免串数据）
func (l *PostListLogic) PostList(req *types.PostListRequest) (resp *types.PostListResponse, err error) {
	// 1. 分页参数兜底
	page := req.Page
	pageSize := req.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	offset := (page - 1) * pageSize
	postType := req.Type
	// 合法 type 白名单：1二手 2租房 3招聘 4求助 9其他
	if postType != 0 && postType != 1 && postType != 2 && postType != 3 && postType != 4 && postType != 9 {
		postType = 0 // 非法值兜底为"全部"
	}
	// 过滤范围:city 优先(只能用固定 SQL,不能用变量拼接列名 — sqlx 不支持)
	adcode := req.AreaAdcode
	if req.CityAdcode != "" {
		adcode = req.CityAdcode
	}
	// 关键词规范化：空串 = 不搜索(正常浏览)；过短(单字) = 搜不了,返回空列表
	//   - ngram 默认 token_size=2,单字查询恒命中 0 行
	//   - 若把单字当"不搜索"处理,用户会看到全量列表,以为搜索没生效
	keyword := search.Normalize(req.Keyword)
	tooShort := search.TooShort(keyword)
	if tooShort {
		// 关键词太短:直接返回空列表(不进 DB,也不写缓存——这是异常输入不是可缓存结果)
		return &types.PostListResponse{
			List:  []types.PostItem{},
			Total: 0,
		}, nil
	}
	searching := search.IsValid(keyword)
	// BOOLEAN 查询串：多词用 + 要求全部命中（AND），单词原样必含
	var boolQuery string
	if searching {
		boolQuery = search.BuildBooleanQuery(keyword)
	}
	kwHash := search.CacheKeyHash(keyword)

	// 2. 查缓存（按 type + adcode + keyword + page + size 分桶）
	cacheKey := postListCacheKey(postType, adcode, kwHash, page, pageSize)
	cached, cacheErr := l.svcCtx.RedisCli.Get(cacheKey)
	if cacheErr == nil && cached != "" {
		var hit types.PostListResponse
		if jsonUnmarshalErr := json.Unmarshal([]byte(cached), &hit); jsonUnmarshalErr == nil {
			return &hit, nil
		} else {
			l.Errorf("PostList 缓存反序列化失败: key=%s, err=%v", cacheKey, jsonUnmarshalErr)
		}
	}

	// 3. 动态拼 COUNT SQL：type=0 不加 type 过滤；adcode="" 不加 adcode 过滤
	//    搜索模式额外加 MATCH ... AGAINST 条件（COUNT 里的 MATCH 也走全文索引）
	var total int64
	var countSQL string
	var countArgs []any
	{
		countSQL = "SELECT COUNT(*) FROM `post_info` WHERE `status` = 2"
		countArgs = []any{}
		if postType != 0 {
			countSQL += " AND `type` = ?"
			countArgs = append(countArgs, postType)
		}
		if adcode != "" {
			if req.CityAdcode != "" {
				countSQL += " AND `city_adcode` = ?"
			} else {
				countSQL += " AND `area_adcode` = ?"
			}
			countArgs = append(countArgs, adcode)
		}
		if searching {
			countSQL += " AND MATCH(`title`, `content`) AGAINST (? IN BOOLEAN MODE)"
			countArgs = append(countArgs, boolQuery)
		}
	}
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &total, countSQL, countArgs...); err != nil {
		l.Errorf("查询 PostList 总数失败: type=%d, adcode=%s, keyword=%s, err=%v", postType, adcode, keyword, err)
		return nil, errorx.ErrInternal
	}

	// 4. 空结果直接返回（同时缓存空结果防穿透）
	if total == 0 {
		resp = &types.PostListResponse{
			List:  []types.PostItem{},
			Total: 0,
		}
		l.cachePostList(cacheKey, resp)
		return resp, nil
	}

	// 5. 分页查询（JOIN user 拿 nickname + avatar）
	//    view_count 不再 SELECT：业务展示直接读 Redis incr 累加值（详见 background.ReadViewCount）
	//    动态拼 WHERE 条件：type / adcode / keyword 任意可选
	//    搜索模式额外 SELECT 一列 MATCH 相关度（search_score）供排序用
	listSQL := `
		SELECT
			p.id, p.user_id,
			u.nickname, u.avatar,
			p.type, p.title, p.images, p.content,
			p.area, p.latitude, p.longitude, p.contact,
			p.status,
			DATE_FORMAT(p.created_at, '%Y-%m-%d %H:%i:%s') AS created_at,
			p.area_adcode, p.area_name, p.street_name, p.city_adcode
	`
	listArgs := []any{}
	if searching {
		// 搜索模式：多选一列 MATCH 相关度
		listSQL += ", MATCH(p.title, p.content) AGAINST (? IN BOOLEAN MODE) AS search_score"
		listArgs = append(listArgs, boolQuery)
	} else {
		// 非搜索模式：补一列常量 0，保证 SELECT 列数与 postListRow 字段数恒等
		//   （go-zero sqlx strict mode 要求列数 == struct 字段数）
		listSQL += ", 0 AS search_score"
	}
	listSQL += `
		FROM post_info p
		LEFT JOIN user u ON u.id = p.user_id
		WHERE p.status = 2
	`
	if postType != 0 {
		listSQL += " AND p.type = ?"
		listArgs = append(listArgs, postType)
	}
	if adcode != "" {
		if req.CityAdcode != "" {
			listSQL += " AND p.city_adcode = ?"
		} else {
			listSQL += " AND p.area_adcode = ?"
		}
		listArgs = append(listArgs, adcode)
	}
	if searching {
		listSQL += " AND MATCH(p.title, p.content) AGAINST (? IN BOOLEAN MODE)"
		listArgs = append(listArgs, boolQuery)
	}
	if searching {
		// 搜索模式：相关度优先，同分按时间新的在前
		listSQL += " ORDER BY search_score DESC, p.created_at DESC LIMIT ? OFFSET ?"
	} else {
		listSQL += " ORDER BY p.created_at DESC LIMIT ? OFFSET ?"
	}
	listArgs = append(listArgs, pageSize, offset)

	var rows []postListRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &rows, listSQL, listArgs...); err != nil {
		l.Errorf("查询 PostList 列表失败: type=%d, adcode=%s, err=%v", postType, adcode, err)
		return nil, errorx.ErrInternal
	}

	// 6. Avatar: file_key → URL(永久有效，无需签名)
	// 7. 映射到 PostItem（view_count 直接读 Redis incr 累加值，业务展示以 Redis 为准）
	items := make([]types.PostItem, len(rows))
	for i, r := range rows {
		viewCount := background.ReadViewCount(l.svcCtx.RedisCli, background.PostViewKeyPrefix, r.Id)
		items[i] = types.PostItem{
			Id:         int64(r.Id),
			UserId:     int64(r.UserId),
			Nickname:   r.Nickname.String,
			Avatar:     l.svcCtx.OSS.Client.URL(r.Avatar.String),
			Type:       int(r.Type),
			Title:      r.Title,
			Images:     BuildURLsInJSON(l.ctx, r.Images.String, l.svcCtx.OSS.Client),
			Content:    r.Content.String,
			Area:       r.Area,
			Latitude:   r.Latitude,
			Longitude:  r.Longitude,
			Contact:    r.Contact,
			Status:     int(r.Status),
			ViewCount:  viewCount,
			CreatedAt:  r.CreatedAt,
			AreaAdcode: r.AreaAdcode,
			AreaName:   r.AreaName,
			StreetName: r.StreetName,
			CityAdcode: r.CityAdcode,
		}
	}

	resp = &types.PostListResponse{
		List:  items,
		Total: total,
	}

	// 7. 写缓存
	l.cachePostList(cacheKey, resp)

	return resp, nil
}

// cachePostList 写入 PostList 缓存
func (l *PostListLogic) cachePostList(key string, resp *types.PostListResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		l.Errorf("PostList 缓存序列化失败: key=%s, err=%v", key, err)
		return
	}
	if setErr := l.svcCtx.RedisCli.Setex(key, string(data), postListCacheTTLSeconds); setErr != nil {
		l.Errorf("PostList 缓存写入失败: key=%s, err=%v", key, setErr)
	}
}
