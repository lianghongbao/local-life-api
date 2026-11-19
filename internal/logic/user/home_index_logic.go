package user

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"local-life-api/internal/background"
	"local-life-api/internal/keys"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

// 首页各模块取数限制
const (
	homeNoticeLimit       = 5  // 首页公告 5 条
	homeBannerLimit       = 5  // 轮播图 5 张
	homeShopLimit         = 6  // 精选商家 6 条
	topShopCandidateLimit = 30 // 精选商家候选池大小（远大于 6，配合 redis 排序取真正 top N）
)

// 首页缓存 TTL（30s）
const homeIndexCacheTTLSeconds = 30

// homeIndexCacheKeyFor 按 (scope, adcode) 生成缓存 key
//   - scope = "city"  按 city_adcode 过滤(同城内容)
//   - scope = "area"  按 area_adcode 过滤(某区内容)
//   - adcode 空 → 全市(无范围过滤)
//
// 前缀由 internal/keys.Key 加 —— 共享底座的 Redis 上还有 chatroom 在跑。
func homeIndexCacheKeyFor(scope, adcode string) string {
	if adcode == "" {
		return keys.Key("home:index:v1:all")
	}
	return keys.Key("home:index:v1:" + scope + ":" + adcode)
}

// noticeRow 公告精简字段
type noticeRow struct {
	Id    int64  `db:"id"`
	Title string `db:"title"`
}

// bannerRow 轮播图精简字段
type bannerRow struct {
	Id         int64  `db:"id"`
	Image      string `db:"image"`
	JumpType   int64  `db:"jump_type"`
	JumpTarget string `db:"jump_target"`
}

// shopRow 商家精简字段
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
	AreaName    sql.NullString `db:"area_name"`
	AreaAdcode  string         `db:"area_adcode"`
	StreetName  string         `db:"street_name"`
	CityAdcode  string         `db:"city_adcode"`
	CreatedAt   string         `db:"created_at"`
}

type HomeIndexLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewHomeIndexLogic(ctx context.Context, svcCtx *svc.ServiceContext) *HomeIndexLogic {
	return &HomeIndexLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// HomeIndex C 端首页聚合接口（公开接口，无需登录）
//
//  1. 查 Redis 缓存（按 area_adcode 分桶：空=全市；6位数字=某区）
//  2. 未命中：DB 查公告 + 轮播图 + 精选商家（精选商家按 adcode 过滤）
//  3. 写 Redis 缓存（30s TTL）
func (l *HomeIndexLogic) HomeIndex(req *types.HomeRequest) (resp *types.HomeResponse, err error) {
	// 0. 兜底（理论上 handler 不会传 nil，但保护一下）
	if req == nil {
		req = &types.HomeRequest{}
	}

	// 0. 过滤范围确定:city_adcode 优先,降级到 area_adcode
	//   - city 优先:同城内容(整个城市的帖子)
	//   - 只有 area:某区内容(更精确)
	//   - 都没:全市
	filterAdcode := req.AreaAdcode
	filterScope := "area"
	if req.CityAdcode != "" {
		filterAdcode = req.CityAdcode
		filterScope = "city"
	}

	// 1. 缓存 key 按 (scope, adcode) 分桶
	cacheKey := homeIndexCacheKeyFor(filterScope, filterAdcode)
	cached, cacheErr := l.svcCtx.RedisCli.Get(cacheKey)
	if cacheErr == nil && cached != "" {
		var hit types.HomeResponse
		if err := json.Unmarshal([]byte(cached), &hit); err == nil {
			return &hit, nil
		} else {
			l.Errorf("HomeIndex 缓存反序列化失败: key=%s, err=%v", cacheKey, err)
		}
	}

	// 2. 查公告（status=1，按置顶+时间倒序）
	notices, err := l.queryNotices(l.ctx)
	if err != nil {
		l.Errorf("查询首页公告失败: err=%v", err)
		return nil, err
	}

	// 3. 查轮播图（status=1，按 sort_order ASC）
	banners, err := l.queryBanners(l.ctx)
	if err != nil {
		l.Errorf("查询首页轮播图失败: err=%v", err)
		return nil, err
	}

	// 4. 查精选商家（status=2，按 view_count DESC 取前 6；按 city/area_adcode 过滤）
	shops, err := l.queryTopShops(l.ctx, filterScope, filterAdcode)
	if err != nil {
		l.Errorf("查询首页商家失败: err=%v", err)
		return nil, err
	}

	resp = &types.HomeResponse{
		Notices: notices,
		Banners: banners,
		Shops:   shops,
	}

	// 5. 写缓存（分桶 key）
	data, _ := json.Marshal(resp)
	if setErr := l.svcCtx.RedisCli.Setex(cacheKey, string(data), homeIndexCacheTTLSeconds); setErr != nil {
		l.Errorf("HomeIndex 缓存写入失败: key=%s, err=%v", cacheKey, setErr)
	}

	return resp, nil
}

// filterValidBanners 过滤掉跳转目标已失效的 banner（帖子/商家被拒绝或删除）
func (l *HomeIndexLogic) filterValidBanners(ctx context.Context, rows []bannerRow) []bannerRow {
	// 按跳转类型分组收集 IDs
	postIDs := make([]int64, 0)
	shopIDs := make([]int64, 0)
	type indexedRow struct {
		idx int
		row bannerRow
	}
	postRows := make([]indexedRow, 0)
	shopRows := make([]indexedRow, 0)

	for i, r := range rows {
		switch r.JumpType {
		case 0: // 无跳转，始终有效
		case 3: // 信息
			id, err := strconv.ParseInt(r.JumpTarget, 10, 64)
			if err == nil && id > 0 {
				postIDs = append(postIDs, id)
				postRows = append(postRows, indexedRow{idx: i, row: r})
			}
		case 2: // 商家
			id, err := strconv.ParseInt(r.JumpTarget, 10, 64)
			if err == nil && id > 0 {
				shopIDs = append(shopIDs, id)
				shopRows = append(shopRows, indexedRow{idx: i, row: r})
			}
		case 1, 4: // 公告/外链，检查公告 status
		}
	}

	// 批量查有效 post（status=2）
	validPosts := make(map[int64]bool)
	if len(postIDs) > 0 {
		placeholders := strings.Repeat("?,", len(postIDs)-1) + "?"
		args := make([]any, len(postIDs))
		for i, id := range postIDs {
			args[i] = id
		}
		var valid []int64
		query := "SELECT id FROM `post_info` WHERE id IN (" + placeholders + ") AND status = 2"
		if err := l.svcCtx.DBConn.QueryRowsCtx(ctx, &valid, query, args...); err == nil {
			for _, id := range valid {
				validPosts[id] = true
			}
		}
	}

	// 批量查有效 shop（status=2）
	validShops := make(map[int64]bool)
	if len(shopIDs) > 0 {
		placeholders := strings.Repeat("?,", len(shopIDs)-1) + "?"
		args := make([]any, len(shopIDs))
		for i, id := range shopIDs {
			args[i] = id
		}
		var valid []int64
		query := "SELECT id FROM `shop` WHERE id IN (" + placeholders + ") AND status = 2"
		if err := l.svcCtx.DBConn.QueryRowsCtx(ctx, &valid, query, args...); err == nil {
			for _, id := range valid {
				validShops[id] = true
			}
		}
	}

	// 过滤
	result := make([]bannerRow, 0, len(rows))
	for _, r := range rows {
		switch r.JumpType {
		case 0, 1, 4: // 无跳转 / 公告(已查 status) / 外链
			result = append(result, r)
		case 3: // 信息
			id, _ := strconv.ParseInt(r.JumpTarget, 10, 64)
			if validPosts[id] {
				result = append(result, r)
			}
		case 2: // 商家
			id, _ := strconv.ParseInt(r.JumpTarget, 10, 64)
			if validShops[id] {
				result = append(result, r)
			}
		}
	}
	return result
}

func (l *HomeIndexLogic) queryNotices(ctx context.Context) ([]types.HomeNoticeItem, error) {
	query := `
		SELECT id, title
		FROM notice
		WHERE status = 1
		ORDER BY is_top DESC, created_at DESC
		LIMIT ?
	`
	var rows []noticeRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(ctx, &rows, query, homeNoticeLimit); err != nil {
		return nil, err
	}

	items := make([]types.HomeNoticeItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, types.HomeNoticeItem{Id: r.Id, Title: r.Title})
	}
	return items, nil
}

func (l *HomeIndexLogic) queryBanners(ctx context.Context) ([]types.HomeBannerItem, error) {
	query := `
		SELECT id, image, jump_type, jump_target
		FROM banner
		WHERE status = 1
		ORDER BY sort_order ASC
		LIMIT ?
	`
	var rows []bannerRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(ctx, &rows, query, homeBannerLimit); err != nil {
		return nil, err
	}

	// 过滤掉跳转目标已失效的 banner（帖子/商家被拒绝或删除）
	rows = l.filterValidBanners(ctx, rows)

	// image: file_key → URL(永久有效)
	items := make([]types.HomeBannerItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, types.HomeBannerItem{
			Id:         r.Id,
			Image:      l.svcCtx.OSS.Client.URL(r.Image),
			JumpType:   int(r.JumpType),
			JumpTarget: r.JumpTarget,
		})
	}
	return items, nil
}

func (l *HomeIndexLogic) queryTopShops(ctx context.Context, scope, adcode string) ([]types.ShopItem, error) {
	// 数据流：
	//   1. SQL 拉候选商家：按 id 倒序拉 topShopCandidateLimit 条（远多于首页 6 条，避免 redis 排序后真正 top6 落选）
	//   2. 一次 MGET 拿全部 view_count（业务读语义：直接读 Redis incr 累加值）
	//   3. Go 层按 view_count DESC 排序，截取前 homeShopLimit 条
	//
	// 排序实时性：
	//   - mysql.view_count 滞后 30s，按它排序会与用户实际看到的 view_count 数据不一致
	//   - 这里改用 Redis 实时 incr 值排序，确保排序与展示值同源
	//
	// 区域过滤：
	//   - areaAdcode 为空时查全市；否则按 area_adcode 精确过滤
	//   - LIMIT 在 args 末尾，与占位符顺序对齐
	query := `
		SELECT id, name, category, address, latitude, longitude, phone,
		       description, images,
		       status, area_name, area_adcode, street_name, city_adcode,
		       DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') AS created_at
		FROM shop
		WHERE status = 2
	`
	args := []interface{}{}
	if adcode != "" {
		// 用静态列名 — sqlx 不支持动态拼接列名
		if scope == "city" {
			query += " AND `city_adcode` = ? "
		} else {
			query += " AND `area_adcode` = ? "
		}
		args = append(args, adcode)
	}
	query += " ORDER BY id DESC LIMIT ? "
	args = append(args, topShopCandidateLimit)

	var rows []shopRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []types.ShopItem{}, nil
	}

	// 1 次 MGET 拉全部 view_count
	ids := make([]uint64, len(rows))
	for i, r := range rows {
		ids[i] = r.Id
	}
	viewCounts, err := background.ReadViewCountsBatch(l.svcCtx.RedisCli, background.ShopViewKeyPrefix, ids)
	if err != nil {
		// MGET 失败：降级走单条 GET / ReadViewCount（最佳努力）
		l.Infof("[home_index] MGET 失败，降级单条 GET: err=%v", err)
		viewCounts = make(map[uint64]int, len(ids))
		for _, id := range ids {
			viewCounts[id] = background.ReadViewCount(l.svcCtx.RedisCli, background.ShopViewKeyPrefix, id)
		}
	}

	// 应用层排序：按 view_count DESC，同分时按 id DESC
	sorted := make([]shopRow, len(rows))
	copy(sorted, rows)
	sort.Slice(sorted, func(i, j int) bool {
		vi := viewCounts[sorted[i].Id]
		vj := viewCounts[sorted[j].Id]
		if vi != vj {
			return vi > vj
		}
		return sorted[i].Id > sorted[j].Id
	})

	// 截取前 homeShopLimit 条
	if len(sorted) > homeShopLimit {
		sorted = sorted[:homeShopLimit]
	}

	items := make([]types.ShopItem, 0, len(sorted))
	for _, r := range sorted {
		items = append(items, types.ShopItem{
			Id:          int64(r.Id),
			Name:        r.Name,
			Category:    int(r.Category),
			Address:     r.Address,
			Phone:       r.Phone,
			Description: r.Description.String,
			Images:      BuildURLsInJSON(l.ctx, r.Images.String, l.svcCtx.OSS.Client),
			Status:      int(r.Status),
			ViewCount:   viewCounts[r.Id],
			CreatedAt:   r.CreatedAt,
			AreaName:    r.AreaName.String,
			AreaAdcode:  r.AreaAdcode,
			StreetName:  r.StreetName,
			CityAdcode:  r.CityAdcode,
		})
	}
	return items, nil
}
