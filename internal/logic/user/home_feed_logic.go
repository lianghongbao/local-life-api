package user

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"local-life-api/internal/background"
	"local-life-api/internal/cachex"
	"local-life-api/internal/search"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

// ====== 首页本地推荐混合流常量 ======

const (
	// 默认页大小:小店/发帖各占一半(page_size=12 → 6+6)
	homeFeedDefaultPageSize = 12
	// 候选池大小:每个表取候选池,远大于一页 page_size,保证混合排序后真正 topN 落选的不被刷掉
	homeFeedCandidateLimit = 60
	// 时间衰减系数:weight = view_count * exp(-age_days / decayDays)
	homeFeedDecayDays = 7.0
	// 缓存 TTL:60s,内容变化频次低,首页混合流需要新鲜感又不能压库
	homeFeedCacheTTLSeconds = 60
)

// 类目 emoji + name 映射(后端内部维护,与前端 utils/constants.js 保持一致)
//   - shop: 1家政 2维修 3开锁 4搬家 5跑腿 6美业 7教育 8美食 9其他
//   - post: 1二手 2租房 3招聘 4求助 9其他
type catInfo struct {
	Name  string
	Emoji string
}

var shopCategoryMap = map[int64]catInfo{
	1: {"家政", "🧹"},
	2: {"维修", "🔧"},
	3: {"开锁", "🔑"},
	4: {"搬家", "📦"},
	5: {"跑腿", "🏃"},
	6: {"美业", "💇"},
	7: {"教育", "📚"},
	8: {"美食", "🍜"},
	9: {"其他", "🌿"},
}

var postTypeMap = map[int64]catInfo{
	1: {"二手", "🛒"},
	2: {"租房", "🏠"},
	3: {"招聘", "💼"},
	4: {"求助", "🤝"},
	9: {"其他", "🌿"},
}

// ====== feedRow 内部统一结构(混合排序用) ======

type feedRow struct {
	Type      string  // "shop" | "post"
	Id        uint64
	Weight    float64
	CreatedAt time.Time
	// SearchScore 全文检索相关度分(仅搜索模式 > 0),搜索时用作 Weight
	SearchScore float64
	// shop 字段
	ShopName      string
	ShopCover     string
	ShopCategory  int64
	ShopAddress   string
	ShopViewCount int
	ShopNickname  string
	// post 字段
	PostType      int64
	PostTitle     string
	PostCover     string
	PostArea      string
	PostViewCount int
	PostNickname  string
	PostComment   int
}

// ====== Logic ======

type HomeFeedLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewHomeFeedLogic(ctx context.Context, svcCtx *svc.ServiceContext) *HomeFeedLogic {
	return &HomeFeedLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// homeFeedCacheKey 按 (scope, adcode, version, kwHash, page, size) 分桶 key
//   - scope = "city"  按 city_adcode 过滤(同城内容)
//   - scope = "area"  按 area_adcode 过滤(某区内容)
//   - adcode 空 → 全市(不参与 version,version=0)
//   - kwHash = 关键词哈希(search.CacheKeyHash),空关键词时为 "0"
//     必须参与 key：否则搜"二手"会命中搜"租房"的首页缓存
func homeFeedCacheKey(scope, adcode string, version int64, kwHash string, page, pageSize int) string {
	if kwHash == "" {
		kwHash = "0"
	}
	return fmt.Sprintf("home:feed:v1:%s:%s:ver%d:kw%s:p%d:s%d", scope, adcode, version, kwHash, page, pageSize)
}

// HomeFeed 首页本地推荐混合流（C 端公开接口，无需登录）
//
//  数据流:
//   1. 缓存按 (scope, adcode, page, size) 分桶,TTL 60s
//   2. 未命中:查 shop 候选池(60 条) + post_info 候选池(60 条)
//   3. 一次 HMGET 拉全部 view_count(走 Redis 实时值,与展示同源)
//   4. 算权重:weight = view_count * exp(-age_days / 7.0)
//   5. 合并排序(weight desc, id desc tie-break),按 page/page_size 分页
//   6. 填充类目 emoji/name,组装 HomeFeedItem,写缓存
//
//  设计要点:
//   - 两边各取候选池 60 条,远大于一页 12 条;混合排序后再截,保证真正 topN 不被某个表"挤掉"
//   - view_count 不读 mysql(view_count 滞后 30s),改走 Redis 实时 incr 累加值
//   - adcode 过滤:city 优先 > area > 空(全市),与现有 HomeIndex / PostList 一致
//   - 类型相关字段(shop_* / post_*)用 json:"optional" 标签,前端按 type 字段分发渲染
//   - keyword 搜索:非空时 shop/post 两个候选池都按 FULLTEXT 过滤,
//     且改用相关度排序(搜索命中优先),不再用 view_count×时间衰减
func (l *HomeFeedLogic) HomeFeed(req *types.HomeFeedRequest) (resp *types.HomeFeedResponse, err error) {
	// 0. 兜底
	if req == nil {
		req = &types.HomeFeedRequest{}
	}
	page := int(req.Page)
	pageSize := int(req.PageSize)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = homeFeedDefaultPageSize
	}

	// 1. 过滤范围:city 优先 > area > 空
	filterAdcode := req.AreaAdcode
	filterScope := "area"
	if req.CityAdcode != "" {
		filterAdcode = req.CityAdcode
		filterScope = "city"
	}

	// 2. 关键词规范化:空串 = 不搜索(正常浏览);过短(单字) = 搜不了,返回空列表
	//   - ngram 默认 token_size=2,单字查询恒命中 0 行
	//   - 若把单字当"不搜索"处理,用户会看到全量混合流,以为搜索没生效
	keyword := search.Normalize(req.Keyword)
	if search.TooShort(keyword) {
		// 关键词太短:直接返回空列表(不进 DB,也不写缓存——这是异常输入不是可缓存结果)
		return &types.HomeFeedResponse{
			List:    []types.HomeFeedItem{},
			HasMore: false,
		}, nil
	}
	searching := search.IsValid(keyword)
	kwHash := search.CacheKeyHash(keyword)

	// 3. 缓存查
	version := cachex.GetFeedVersion(l.ctx, l.svcCtx.RedisCli, filterScope, filterAdcode)
	cacheKey := homeFeedCacheKey(filterScope, filterAdcode, version, kwHash, page, pageSize)
	if cached, cacheErr := l.svcCtx.RedisCli.Get(cacheKey); cacheErr == nil && cached != "" {
		var hit types.HomeFeedResponse
		if jsonErr := json.Unmarshal([]byte(cached), &hit); jsonErr == nil {
			return &hit, nil
		} else {
			l.Errorf("HomeFeed 缓存反序列化失败: key=%s, err=%v", cacheKey, jsonErr)
		}
	}

	// 4. 取候选池
	shops, err := l.queryShopCandidates(l.ctx, filterScope, filterAdcode, keyword)
	if err != nil {
		l.Errorf("HomeFeed 查询店铺候选池失败: err=%v", err)
		return nil, err
	}
	posts, err := l.queryPostCandidates(l.ctx, filterScope, filterAdcode, keyword)
	if err != nil {
		l.Errorf("HomeFeed 查询帖子候选池失败: err=%v", err)
		return nil, err
	}

	// 4. 一次 HMGET 拿全部 view_count（实时 Redis 值）
	shopIDs := make([]uint64, len(shops))
	for i, s := range shops {
		shopIDs[i] = s.Id
	}
	shopVC, err := background.ReadViewCountsBatch(l.svcCtx.RedisCli, background.ShopViewKeyPrefix, shopIDs)
	if err != nil {
		l.Infof("[home_feed] shop MGET 失败,降级单条 GET: err=%v", err)
		shopVC = make(map[uint64]int, len(shopIDs))
		for _, id := range shopIDs {
			shopVC[id] = background.ReadViewCount(l.svcCtx.RedisCli, background.ShopViewKeyPrefix, id)
		}
	}

	postIDs := make([]uint64, len(posts))
	for i, p := range posts {
		postIDs[i] = p.Id
	}
	postVC, err := background.ReadViewCountsBatch(l.svcCtx.RedisCli, background.PostViewKeyPrefix, postIDs)
	if err != nil {
		l.Infof("[home_feed] post MGET 失败,降级单条 GET: err=%v", err)
		postVC = make(map[uint64]int, len(postIDs))
		for _, id := range postIDs {
			postVC[id] = background.ReadViewCount(l.svcCtx.RedisCli, background.PostViewKeyPrefix, id)
		}
	}

	// 5. 算权重
	//    搜索模式:权重 = MATCH 相关度(搜索命中优先,与浏览量无关)
	//    非搜索模式:权重 = view_count × exp(-age_days / 7.0)(时间衰减)
	now := time.Now()
	all := make([]feedRow, 0, len(shops)+len(posts))
	for _, s := range shops {
		vc := shopVC[s.Id]
		if searching {
			s.Weight = s.SearchScore
		} else {
			s.Weight = calcWeight(vc, now.Sub(s.CreatedAt))
		}
		all = append(all, s)
	}
	for _, p := range posts {
		vc := postVC[p.Id]
		if searching {
			p.Weight = p.SearchScore
		} else {
			p.Weight = calcWeight(vc, now.Sub(p.CreatedAt))
		}
		all = append(all, p)
	}

	// 6. 合并排序:weight desc,同分按 id desc
	//    搜索模式下 weight 是相关度分,跨表可比(MySQL 同一次查询算出的 MATCH 分数)
	sort.Slice(all, func(i, j int) bool {
		if all[i].Weight != all[j].Weight {
			return all[i].Weight > all[j].Weight
		}
		return all[i].Id > all[j].Id
	})

	// 7. 分页截取
	start := (page - 1) * pageSize
	end := start + pageSize
	if start >= len(all) {
		// 越界:返回空列表 + has_more=false
		resp = &types.HomeFeedResponse{
			List:    []types.HomeFeedItem{},
			HasMore: false,
		}
		l.cacheHomeFeed(cacheKey, resp)
		return resp, nil
	}
	if end > len(all) {
		end = len(all)
	}
	pageRows := all[start:end]
	hasMore := end < len(all)

	// 8. 组装响应
	items := make([]types.HomeFeedItem, 0, len(pageRows))
	for _, r := range pageRows {
		item := types.HomeFeedItem{
			Type:   r.Type,
			Weight: r.Weight,
		}
		if r.Type == "shop" {
			info := shopCategoryMap[r.ShopCategory]
			item.ShopId = int64(r.Id)
			item.ShopName = r.ShopName
			item.ShopCover = l.svcCtx.OSS.Client.URL(r.ShopCover)
			item.ShopCategory = int(r.ShopCategory)
			item.ShopCatName = info.Name
			item.ShopCatEmoji = info.Emoji
			item.ShopAddress = r.ShopAddress
			item.ShopViewCount = shopVC[r.Id]
			item.ShopNickname = r.ShopNickname
		} else {
			info := postTypeMap[r.PostType]
			item.PostId = int64(r.Id)
			item.PostType = int(r.PostType)
			item.PostTypeName = info.Name
			item.PostTypeEmoji = info.Emoji
			item.PostTitle = r.PostTitle
			item.PostCover = l.svcCtx.OSS.Client.URL(r.PostCover)
			item.PostArea = r.PostArea
			item.PostViewCount = postVC[r.Id]
			item.PostNickname = r.PostNickname
			item.PostCommentCount = r.PostComment
		}
		items = append(items, item)
	}

	resp = &types.HomeFeedResponse{
		List:    items,
		HasMore: hasMore,
	}

	// 9. 写缓存
	l.cacheHomeFeed(cacheKey, resp)

	return resp, nil
}

// calcWeight 时间衰减权重:view_count × exp(-age_days / decayDays)
//   - 新内容(age=0) 权重 = view_count
//   - 7 天前       权重 ≈ view_count × 0.37
//   - 30 天前      权重 ≈ view_count × 0.013
func calcWeight(viewCount int, age time.Duration) float64 {
	if viewCount <= 0 {
		return 0
	}
	ageDays := age.Hours() / 24.0
	decay := math.Exp(-ageDays / homeFeedDecayDays)
	return float64(viewCount) * decay
}

// cacheHomeFeed 写入混合流缓存
func (l *HomeFeedLogic) cacheHomeFeed(key string, resp *types.HomeFeedResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		l.Errorf("HomeFeed 缓存序列化失败: key=%s, err=%v", key, err)
		return
	}
	if setErr := l.svcCtx.RedisCli.Setex(key, string(data), homeFeedCacheTTLSeconds); setErr != nil {
		l.Errorf("HomeFeed 缓存写入失败: key=%s, err=%v", key, setErr)
	}
}

// queryShopCandidates 查 shop 候选池(status=2,view_count DESC LIMIT 60)
//   - 排序先在 DB 层用 mysql.view_count(粗排),再在 Go 层用 Redis 实时值精排
//   - adcode 过滤走 city_adcode(优先)/area_adcode,与 HomeIndex / ShopList 一致
//   - 封面:shop 表没有 cover 列,只有 images(JSON 数组字符串),用 extractFirstImage 取第一张
//   - nickname:shop 表没有昵称,LEFT JOIN user 表取创建者昵称(LEFT JOIN 兼容用户已注销的情况)
//   - keyword 非空:按 FULLTEXT 搜店名+地址+介绍,并按相关度排(候选池取相关度 topN)
func (l *HomeFeedLogic) queryShopCandidates(ctx context.Context, scope, adcode string, keyword string) ([]feedRow, error) {
	searching := keyword != ""
	var boolQuery string
	if searching {
		boolQuery = search.BuildBooleanQuery(keyword)
	}
	query := `
		SELECT s.id, s.name, s.category, s.address, s.images, s.view_count,
		       u.nickname, s.created_at
	`
	args := []interface{}{}
	if searching {
		query += ", MATCH(s.name, s.address, s.description) AGAINST (? IN BOOLEAN MODE) AS search_score"
		args = append(args, boolQuery)
	} else {
		// 非搜索模式补常量 0,保证两条路径 SELECT 列数一致
		query += ", 0 AS search_score"
	}
	query += `
		FROM shop s
		LEFT JOIN user u ON u.id = s.user_id
		WHERE s.status = 2
	`
	if adcode != "" {
		if scope == "city" {
			query += " AND s.city_adcode = ?"
		} else {
			query += " AND s.area_adcode = ?"
		}
		args = append(args, adcode)
	}
	if searching {
		query += " AND MATCH(s.name, s.address, s.description) AGAINST (? IN BOOLEAN MODE)"
		args = append(args, boolQuery)
		query += " ORDER BY search_score DESC, s.view_count DESC, s.id DESC LIMIT ?"
	} else {
		query += " ORDER BY s.view_count DESC, s.id DESC LIMIT ?"
	}
	args = append(args, homeFeedCandidateLimit)

	type shopRow struct {
		Id        uint64         `db:"id"`
		Name      string         `db:"name"`
		Category  int64          `db:"category"`
		Address   string         `db:"address"`
		Images    sql.NullString `db:"images"`
		ViewCount uint64         `db:"view_count"`
		// LEFT JOIN user,user 被注销或 nickname 为 NULL 时需兼容
		Nickname   sql.NullString `db:"nickname"`
		CreatedAt  time.Time      `db:"created_at"`
		SearchScore float64       `db:"search_score"`
	}

	var rows []shopRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(ctx, &rows, query, args...); err != nil {
		return nil, err
	}

	out := make([]feedRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, feedRow{
			Type:          "shop",
			Id:            r.Id,
			CreatedAt:     r.CreatedAt,
			SearchScore:   r.SearchScore,
			ShopName:      r.Name,
			ShopCover:     extractFirstImage(r.Images.String), // 从 images JSON 数组取第一张
			ShopCategory:  r.Category,
			ShopAddress:   r.Address,
			ShopViewCount: int(r.ViewCount),
			ShopNickname:  r.Nickname.String,
		})
	}
	return out, nil
}

// queryPostCandidates 查 post_info 候选池(status=2,view_count DESC LIMIT 60)
//   - 同样 LEFT JOIN user 取昵称;与 queryShopCandidates 保持一致
//   - keyword 非空:按 FULLTEXT 搜标题+正文,并按相关度排(候选池取相关度 topN)
func (l *HomeFeedLogic) queryPostCandidates(ctx context.Context, scope, adcode string, keyword string) ([]feedRow, error) {
	searching := keyword != ""
	var boolQuery string
	if searching {
		boolQuery = search.BuildBooleanQuery(keyword)
	}
	query := `
		SELECT p.id, p.type, p.title, p.images, p.area, p.view_count, p.comment_count,
		       u.nickname, p.created_at
	`
	args := []interface{}{}
	if searching {
		query += ", MATCH(p.title, p.content) AGAINST (? IN BOOLEAN MODE) AS search_score"
		args = append(args, boolQuery)
	} else {
		// 非搜索模式补常量 0,保证两条路径 SELECT 列数一致
		query += ", 0 AS search_score"
	}
	query += `
		FROM post_info p
		LEFT JOIN user u ON u.id = p.user_id
		WHERE p.status = 2
	`
	if adcode != "" {
		if scope == "city" {
			query += " AND p.city_adcode = ?"
		} else {
			query += " AND p.area_adcode = ?"
		}
		args = append(args, adcode)
	}
	if searching {
		query += " AND MATCH(p.title, p.content) AGAINST (? IN BOOLEAN MODE)"
		args = append(args, boolQuery)
		query += " ORDER BY search_score DESC, p.view_count DESC, p.id DESC LIMIT ?"
	} else {
		query += " ORDER BY p.view_count DESC, p.id DESC LIMIT ?"
	}
	args = append(args, homeFeedCandidateLimit)

	type postRow struct {
		Id           uint64         `db:"id"`
		Type         int64          `db:"type"`
		Title        string         `db:"title"`
		Images       sql.NullString `db:"images"`
		Area         string         `db:"area"`
		ViewCount    uint64         `db:"view_count"`
		CommentCount uint64         `db:"comment_count"`
		// LEFT JOIN user,user 被注销或 nickname 为 NULL 时需兼容
		Nickname     sql.NullString `db:"nickname"`
		CreatedAt    time.Time      `db:"created_at"`
		SearchScore  float64        `db:"search_score"`
	}

	var rows []postRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(ctx, &rows, query, args...); err != nil {
		return nil, err
	}

	out := make([]feedRow, 0, len(rows))
	for _, r := range rows {
		// 取 images JSON 数组的第一张作封面
		cover := extractFirstImage(r.Images.String)
		out = append(out, feedRow{
			Type:          "post",
			Id:            r.Id,
			CreatedAt:     r.CreatedAt,
			SearchScore:   r.SearchScore,
			PostType:      r.Type,
			PostTitle:     r.Title,
			PostCover:     cover,
			PostArea:      r.Area,
			PostViewCount: int(r.ViewCount),
			PostNickname:  r.Nickname.String,
			PostComment:   int(r.CommentCount),
		})
	}
	return out, nil
}

// extractFirstImage 从 post_info.images(JSON 数组字符串)取第一张
//   - 数组为空/解析失败 → 返回空字符串(前端按 type 字段跳过封面)
func extractFirstImage(imagesJSON string) string {
	if imagesJSON == "" {
		return ""
	}
	var arr []string
	if err := json.Unmarshal([]byte(imagesJSON), &arr); err != nil {
		return ""
	}
	if len(arr) == 0 {
		return ""
	}
	return arr[0]
}