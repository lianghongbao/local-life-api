// 同城推荐:镇/街道列表 + Redis 24h 缓存
//   - 数据源:post_info + shop 聚合(UNION ALL 后 GROUP BY street_name)
//   - 缓存粒度:按 area_adcode 隔离,key = "geo:streets:v1:<area_adcode>"
//   - TTL:24h(数据量小且变化不频繁,过期重建代价低)

package user

import (
	"context"
	"encoding/json"
	"fmt"

	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

const (
	geoStreetsCacheTTL = 24 * 3600
	geoStreetsKeyFmt   = "local-life:geo:streets:v1:%s"
)

type GeoStreetsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewGeoStreetsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GeoStreetsLogic {
	return &GeoStreetsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// GeoStreets 列出某 area_adcode 下所有出现过的镇/街道(post_count + shop_count 聚合)
//   - 空入参短路:避免无意义 DB 查询
//   - 缓存命中直接返回 JSON 反序列化的结果
//   - 缓存未命中走 DB 聚合,写回 cache
func (l *GeoStreetsLogic) GeoStreets(req *types.GeoStreetsReq) (resp *types.GeoStreetsResp, err error) {
	if req.AreaAdcode == "" {
		return &types.GeoStreetsResp{Items: []types.GeoStreetItem{}}, nil
	}
	key := fmt.Sprintf(geoStreetsKeyFmt, req.AreaAdcode)

	// 1. 缓存命中
	if cached, _ := l.svcCtx.RedisCli.Get(key); cached != "" {
		var hit []types.GeoStreetItem
		if json.Unmarshal([]byte(cached), &hit) == nil {
			return &types.GeoStreetsResp{Items: hit}, nil
		}
		l.Errorf("[geo_streets] cache unmarshal failed, key=%s", key)
	}

	// 2. DB 聚合
	items, err := l.queryStreets(req.AreaAdcode)
	if err != nil {
		l.Errorf("[geo_streets] query failed: area=%s err=%v", req.AreaAdcode, err)
		return nil, err
	}

	// 3. 写缓存(失败不影响主流程)
	if data, mErr := json.Marshal(items); mErr == nil {
		_ = l.svcCtx.RedisCli.Setex(key, string(data), geoStreetsCacheTTL)
	}

	return &types.GeoStreetsResp{Items: items}, nil
}

// queryStreets 聚合 post_info + shop,按 (post_count + shop_count) 倒序取前 50 个镇/街道
//   过滤 street_name = '' 的脏数据(回填未完成时会污染列表)
func (l *GeoStreetsLogic) queryStreets(areaAdcode string) ([]types.GeoStreetItem, error) {
	query := `
		SELECT street_name,
		       SUM(post_count) AS post_count,
		       SUM(shop_count) AS shop_count
		FROM (
			SELECT street_name, COUNT(*) AS post_count, 0 AS shop_count
			FROM post_info
			WHERE status = 2 AND area_adcode = ? AND street_name <> ''
			GROUP BY street_name
			UNION ALL
			SELECT street_name, 0 AS post_count, COUNT(*) AS shop_count
			FROM shop
			WHERE status = 2 AND area_adcode = ? AND street_name <> ''
			GROUP BY street_name
		) t
		GROUP BY street_name
		ORDER BY (post_count + shop_count) DESC
		LIMIT 50
	`
	type row struct {
		StreetName string `db:"street_name"`
		PostCount  int    `db:"post_count"`
		ShopCount  int    `db:"shop_count"`
	}
	var rows []row
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &rows, query, areaAdcode, areaAdcode); err != nil {
		return nil, err
	}
	out := make([]types.GeoStreetItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, types.GeoStreetItem{
			StreetName: r.StreetName,
			PostCount:  r.PostCount,
			ShopCount:  r.ShopCount,
		})
	}
	return out, nil
}