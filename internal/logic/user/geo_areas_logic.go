// 同城推荐:区列表
//   - 数据源:
//     1) 腾讯 /list 拿全市 + 全区(result[1]/result[2])
//     2) 腾讯 /search 按关键词搜区
//     3) DB 聚合拿每个区的 post_count + shop_count(给 picker 排序用)
//   - 缓存粒度:7 天(行政区划) + 24h(业务量)
//
// 腾讯 V1 /list 响应结构:result = [[省], [市], [区], [街道]]
//   - result[1][i].id 是市级 adcode
//   - result[2][j].id 是区级 adcode
//   - 区没有"属于哪个市"的字段,只能靠 LIKE 模糊匹配(粗)

package user

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"

	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

const (
	geoAreasCacheTTL = 1 * 3600 // 1h:临时缩短,等业务稳定后再改回 7d
	geoAreasKeyFmt   = "local-life:geo:areas:v1:%s" // city_adcode | "search:<kw>"

	areasTencentDistrictURL = "https://apis.map.qq.com/ws/district/v1/"
	areasTencentFailTTL     = 5 * 60
	areasTencentFailKey     = "local-life:geo:areas:v1:fail"
	areasTencentTimeout     = 3 * time.Second
)

type GeoAreasLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewGeoAreasLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GeoAreasLogic {
	return &GeoAreasLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// GeoAreas 入口:keyword 优先
func (l *GeoAreasLogic) GeoAreas(req *types.GeoAreasReq) (resp *types.GeoAreasResp, err error) {
	if req.CityAdcode == "" && req.Keyword == "" {
		return &types.GeoAreasResp{Items: []types.GeoAreaItem{}}, nil
	}
	if !isTencentMapKeyConfigured(l.svcCtx.TencentMapKey) {
		l.Errorf("[geo_areas] tencent map key not configured")
		return &types.GeoAreasResp{Items: []types.GeoAreaItem{}}, nil
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}

	// 搜索路径
	if req.Keyword != "" {
		cacheKey := fmt.Sprintf(geoAreasKeyFmt, "search:"+req.Keyword)
		if cached, _ := l.svcCtx.RedisCli.Get(cacheKey); cached != "" {
			var hit []types.GeoAreaItem
			if json.Unmarshal([]byte(cached), &hit) == nil {
				return &types.GeoAreasResp{Items: hit}, nil
			}
		}
		items, err := l.searchByKeyword(req.Keyword, limit)
		if err != nil {
			l.Errorf("[geo_areas] search failed: kw=%s err=%v", req.Keyword, err)
			return &types.GeoAreasResp{Items: []types.GeoAreaItem{}}, nil
		}
		if data, mErr := json.Marshal(items); mErr == nil {
			_ = l.svcCtx.RedisCli.Setex(cacheKey, string(data), geoAreasCacheTTL)
		}
		return &types.GeoAreasResp{Items: items}, nil
	}

	// 列出某 city_adcode 的所有区
	cacheKey := fmt.Sprintf(geoAreasKeyFmt, "city:"+req.CityAdcode)
	if cached, _ := l.svcCtx.RedisCli.Get(cacheKey); cached != "" {
		var hit []types.GeoAreaItem
		if json.Unmarshal([]byte(cached), &hit) == nil {
			return &types.GeoAreasResp{Items: hit}, nil
		}
	}

	items, err := l.listByCity(req.CityAdcode, limit)
	if err != nil {
		l.Errorf("[geo_areas] listByCity failed: city=%s err=%v", req.CityAdcode, err)
		return &types.GeoAreasResp{Items: []types.GeoAreaItem{}}, nil
	}

	if data, mErr := json.Marshal(items); mErr == nil {
		_ = l.svcCtx.RedisCli.Setex(cacheKey, string(data), geoAreasCacheTTL)
	}
	return &types.GeoAreasResp{Items: items}, nil
}

// ============== 业务实现 ==============

// tencentDistrictItem 腾讯 V1 行政区划基本字段
type tencentDistrictItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`     // 省/市有,区/街道可能没有
	Fullname string `json:"fullname"`
	Cidx     []int  `json:"cidx"`     // 省/市有,指向 result[2] 区数组的索引区间 [start, end]
}

type tencentListResponse struct {
	Status  int                    `json:"status"`
	Message string                 `json:"message"`
	Result  [][]tencentDistrictItem `json:"result"`
}

// searchByKeyword 用腾讯 /search 搜行政区划
//   搜索结果包含各种级别,过滤出 district 级别(用 fullname 后缀判断:"区"/"县"/"市")
func (l *GeoAreasLogic) searchByKeyword(keyword string, limit int) ([]types.GeoAreaItem, error) {
	raw, err := l.tencentGet("search", url.Values{
		"keyword":   {keyword},
		"page_size": {fmt.Sprintf("%d", limit)},
		"page_index": {"1"},
	})
	if err != nil {
		return nil, err
	}
	var resp tencentListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	if resp.Status != 0 {
		return nil, fmt.Errorf("tencent status=%d msg=%s", resp.Status, resp.Message)
	}

	var matches []tencentDistrictItem
	if len(resp.Result) > 0 {
		matches = resp.Result[0]
	}

	out := make([]types.GeoAreaItem, 0, len(matches))
	for _, m := range matches {
		// 过滤:只保留区/县级别(fullname 以"区"/"县"结尾,且不是直辖市的特殊处理)
		if !isAreaLevel(m.Fullname) {
			continue
		}
		out = append(out, types.GeoAreaItem{
			AreaAdcode: m.ID,
			AreaName:   m.Fullname,
			// CityAdcode/CityName: search 返回没有,前端只能用 picker 上下文兜底
		})
	}
	return out, nil
}

// listByCity 列某市的所有区
//   策略:用腾讯 /list 返回的 cidx 索引区间,直接从 result[2] 切片
//   - 市 adcode 在 result[1] 里,直辖市在 result[0] 里(用省级 adcode 当市级)
//   - cidx 是闭区间 [start, end],指向 result[2] 区数组
//   - 区 fullname 是简称("锦江区"),不带市前缀
func (l *GeoAreasLogic) listByCity(cityAdcode string, limit int) ([]types.GeoAreaItem, error) {
	raw, err := l.tencentGet("list", url.Values{
		"page_size": {"1000"},
	})
	if err != nil {
		return nil, err
	}
	var resp tencentListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	if resp.Status != 0 {
		return nil, fmt.Errorf("tencent status=%d msg=%s", resp.Status, resp.Message)
	}
	if len(resp.Result) < 3 {
		return []types.GeoAreaItem{}, nil
	}

	// 找该市的 cidx + cityName
	var cidx []int
	var cityName string
	for _, city := range resp.Result[1] {
		if city.ID == cityAdcode {
			cidx = city.Cidx
			cityName = city.Fullname
			if cityName == "" {
				cityName = city.Name
			}
			break
		}
	}
	if cidx == nil {
		// 可能在 result[0](直辖市:北京/上海/天津/重庆)
		for _, prov := range resp.Result[0] {
			if prov.ID == cityAdcode {
				cidx = prov.Cidx
				cityName = prov.Fullname
				if cityName == "" {
					cityName = prov.Name
				}
				break
			}
		}
	}
	if len(cidx) < 2 {
		return []types.GeoAreaItem{}, nil
	}

	start, end := cidx[0], cidx[1]
	// 防御:防止 cidx 越界
	if start < 0 || start >= len(resp.Result[2]) {
		return []types.GeoAreaItem{}, nil
	}
	if end >= len(resp.Result[2]) {
		end = len(resp.Result[2]) - 1
	}
	if end < start {
		return []types.GeoAreaItem{}, nil
	}

	out := make([]types.GeoAreaItem, 0, end-start+1)
	for _, area := range resp.Result[2][start : end+1] {
		if !isAreaLevel(area.Fullname) {
			continue
		}
		out = append(out, types.GeoAreaItem{
			AreaAdcode: area.ID,
			AreaName:   area.Fullname,
			CityAdcode: cityAdcode,
			CityName:   cityName,
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].AreaAdcode < out[j].AreaAdcode
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// isAreaLevel 判断 fullname 后缀是否是区/县
//   "朝阳区" / "海淀区" / "通江县" / "都江堰市" → true
//   "北京市" / "成都市" → false
func isAreaLevel(name string) bool {
	if len(name) < 2 {
		return false
	}
	last := name[len(name)-len("区"):]
	if last == "区" || last == "县" {
		return true
	}
	return false
}

// tencentGet 通用 HTTP GET + 失败标记
func (l *GeoAreasLogic) tencentGet(action string, params url.Values) ([]byte, error) {
	if failHit, _ := l.svcCtx.RedisCli.Get(areasTencentFailKey); failHit != "" {
		return nil, fmt.Errorf("tencent fail cached")
	}

	q := url.Values{}
	for k, v := range params {
		q[k] = v
	}
	q.Set("key", l.svcCtx.TencentMapKey)
	u := areasTencentDistrictURL + action + "?" + q.Encode()

	client := &http.Client{Timeout: areasTencentTimeout}
	req, err := http.NewRequestWithContext(l.ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		_ = l.svcCtx.RedisCli.Setex(areasTencentFailKey, "1", areasTencentFailTTL)
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// 检查 status
	var statusCheck struct {
		Status int `json:"status"`
	}
	_ = json.Unmarshal(body, &statusCheck)
	if statusCheck.Status != 0 {
		_ = l.svcCtx.RedisCli.Setex(areasTencentFailKey, "1", areasTencentFailTTL)
		return nil, fmt.Errorf("tencent status=%d", statusCheck.Status)
	}
	return body, nil
}