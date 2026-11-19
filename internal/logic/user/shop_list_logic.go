package user

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"local-life-api/internal/errorx"
	"local-life-api/internal/keys"
	"local-life-api/internal/search"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type ShopListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewShopListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ShopListLogic {
	return &ShopListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// 商家列表缓存 TTL（秒）
const (
	shopListCacheTTLSeconds   = 10
	shopListEmptyCacheTTLSecs = 5
)

// shopListCacheKey 缓存 key：shops:cat{cat}:area{adcode}:kw{kwHash}:p{page}:s{size}
//   - cat=0 表示全部，独立 namespace
//   - adcode="" 表示"全市"，仍作为 key 一部分（独立 namespace）
//   - kwHash=关键词哈希(search.CacheKeyHash)，空关键词时为 "0"
//     必须参与 key：否则搜"家政"会命中搜"维修"的缓存
func shopListCacheKey(category int, adcode string, kwHash string, page, pageSize int) string {
	if kwHash == "" {
		kwHash = "0"
	}
	return keys.Key(fmt.Sprintf("shops:cat%d:area%s:kw%s:p%d:s%d", category, adcode, kwHash, page, pageSize))
}

// ShopList C 端公开商家列表
//   - 仅展示 status=2（已通过）
//   - category=0 全部，否则按 category 精确过滤
//   - adcode 可选：空=全市；非空=按区过滤
//   - keyword 可选：非空时按 MySQL FULLTEXT(ngram) 搜店名+地址+介绍，
//     并改用相关度排序（MATCH score DESC, view_count DESC）
//   - 无 keyword 时按 view_count DESC, id DESC 排序（热门优先）
//   - Redis 缓存 10s，按 (category, adcode, keyword, page, size) 分桶
func (l *ShopListLogic) ShopList(req *types.ShopListRequest) (resp *types.ShopListResponse, err error) {
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
	category := req.Category
	if category < 0 || category > 9 {
		return nil, errorx.ErrInvalidParam
	}
	// 过滤范围:city 优先(不能用变量拼接列名 — sqlx 不支持)
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
		return &types.ShopListResponse{
			List:  []types.ShopItem{},
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

	// 2. 查缓存（按 category + adcode + keyword + page + size 分桶）
	cacheKey := shopListCacheKey(category, adcode, kwHash, page, pageSize)
	cached, cacheErr := l.svcCtx.RedisCli.Get(cacheKey)
	if cacheErr == nil && cached != "" {
		var hit types.ShopListResponse
		if jsonErr := json.Unmarshal([]byte(cached), &hit); jsonErr == nil {
			return &hit, nil
		} else {
			l.Errorf("ShopList 缓存反序列化失败: key=%s, err=%v", cacheKey, jsonErr)
		}
	}

	// 3. 动态拼 COUNT SQL：category=0 不加 category 过滤；adcode="" 不加 adcode 过滤
	//    搜索模式额外加 MATCH ... AGAINST 条件（COUNT 里的 MATCH 也走全文索引）
	var total int64
	var countSQL string
	var countArgs []any
	{
		countSQL = "SELECT COUNT(*) FROM `shop` WHERE `status` = 2"
		countArgs = []any{}
		if category != 0 {
			countSQL += " AND `category` = ?"
			countArgs = append(countArgs, category)
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
			countSQL += " AND MATCH(`name`, `address`, `description`) AGAINST (? IN BOOLEAN MODE)"
			countArgs = append(countArgs, boolQuery)
		}
	}
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &total, countSQL, countArgs...); err != nil {
		l.Errorf("查询 ShopList 总数失败: category=%d, area=%s, keyword=%s, err=%v", category, adcode, keyword, err)
		return nil, errorx.ErrInternal
	}

	// 4. 空结果短路（短 TTL 防穿透 + 快速失效）
	if total == 0 {
		resp = &types.ShopListResponse{
			List:  []types.ShopItem{},
			Total: 0,
		}
		l.cacheShopList(cacheKey, resp, shopListEmptyCacheTTLSecs)
		return resp, nil
	}

	// 5. 分页查询
	//    view_count 不再 SELECT（可选优化：保持与现状一致，仍 SELECT 让排序在 DB 层完成）
	//    动态拼 WHERE 条件：category / adcode / keyword 任意可选
	//    SELECT 区划四字段(area_name/area_adcode/street_name/city_adcode),
	//    让 C 端 list / detail 透出区名 + adcode,后续做"同区推荐"不用再回 DB 查。
	//    搜索模式额外 SELECT 一列 MATCH 相关度（search_score）供排序用
	listSQL := `
		SELECT
			id, name, category, address, latitude, longitude, phone, description, images,
			status, view_count,
			area_name, area_adcode, street_name, city_adcode,
			DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') AS created_at
	`
	listArgs := []any{}
	if searching {
		// 搜索模式：多选一列 MATCH 相关度
		listSQL += ", MATCH(name, address, description) AGAINST (? IN BOOLEAN MODE) AS search_score"
		listArgs = append(listArgs, boolQuery)
	} else {
		// 非搜索模式：补一列常量 0，保证 SELECT 列数与 shopRow 字段数恒等
		listSQL += ", 0 AS search_score"
	}
	listSQL += `
		FROM shop
		WHERE status = 2
	`
	if category != 0 {
		listSQL += " AND category = ?"
		listArgs = append(listArgs, category)
	}
	if adcode != "" {
		if req.CityAdcode != "" {
			listSQL += " AND city_adcode = ?"
		} else {
			listSQL += " AND area_adcode = ?"
		}
		listArgs = append(listArgs, adcode)
	}
	if searching {
		listSQL += " AND MATCH(name, address, description) AGAINST (? IN BOOLEAN MODE)"
		listArgs = append(listArgs, boolQuery)
	}
	if searching {
		// 搜索模式：相关度优先，同分按浏览数多的在前
		listSQL += " ORDER BY search_score DESC, view_count DESC, id DESC LIMIT ? OFFSET ?"
	} else {
		listSQL += " ORDER BY view_count DESC, id DESC LIMIT ? OFFSET ?"
	}
	listArgs = append(listArgs, pageSize, offset)

	type shopRow struct {
		Id          uint64         `db:"id"`
		Name        string         `db:"name"`
		Category    int64          `db:"category"`
		Address     string         `db:"address"`
		Latitude    float64        `db:"latitude"`
		Longitude   float64        `db:"longitude"`
		Phone       string         `db:"phone"`
		Description sql.NullString `db:"description"`
		Images      sql.NullString `db:"images"`
		Status      int64          `db:"status"`
		ViewCount   uint64         `db:"view_count"`
		AreaName    sql.NullString `db:"area_name"`
		AreaAdcode  string         `db:"area_adcode"`
		StreetName  string         `db:"street_name"`
		CityAdcode  string         `db:"city_adcode"`
		CreatedAt   string         `db:"created_at"`
		SearchScore float64        `db:"search_score"`
	}

	var rows []shopRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &rows, listSQL, listArgs...); err != nil {
		l.Errorf("查询 ShopList 列表失败: category=%d, area=%s, err=%v", category, adcode, err)
		return nil, errorx.ErrInternal
	}

	// 6. 映射到 ShopItem
	items := make([]types.ShopItem, len(rows))
	for i, r := range rows {
		items[i] = types.ShopItem{
			Id:          int64(r.Id),
			Name:        r.Name,
			Category:    int(r.Category),
			Address:     r.Address,
			Latitude:    r.Latitude,
			Longitude:   r.Longitude,
			Phone:       r.Phone,
			Description: r.Description.String,
			Images:      BuildURLsInJSON(l.ctx, r.Images.String, l.svcCtx.OSS.Client),
			Status:      int(r.Status),
			ViewCount:   int(r.ViewCount),
			CreatedAt:   r.CreatedAt,
			AreaName:    r.AreaName.String,
			AreaAdcode:  r.AreaAdcode,
			StreetName:  r.StreetName,
			CityAdcode:  r.CityAdcode,
		}
	}

	resp = &types.ShopListResponse{
		List:  items,
		Total: total,
	}

	// 7. 写缓存
	l.cacheShopList(cacheKey, resp, shopListCacheTTLSeconds)

	return resp, nil
}

// cacheShopList 写入商家列表缓存
//   - ttl: 缓存时间（秒），由调用方决定（有数据 vs 空结果不同）
func (l *ShopListLogic) cacheShopList(key string, resp *types.ShopListResponse, ttl int) {
	data, err := json.Marshal(resp)
	if err != nil {
		l.Errorf("ShopList 缓存序列化失败: key=%s, err=%v", key, err)
		return
	}
	if setErr := l.svcCtx.RedisCli.Setex(key, string(data), ttl); setErr != nil {
		l.Errorf("ShopList 缓存写入失败: key=%s, err=%v", key, setErr)
	}
}
