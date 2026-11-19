// 同城推荐:城市/区列表
//   - 数据源:腾讯位置服务 /ws/district/v1/(list/search/getchildren)
//   - 复用 TencentMapKey(和 /geo/reverse 共用一个 key)
//   - 缓存粒度:7 天(行政区划数据长期稳定)
//
// 腾讯 district 接口响应结构(V1):
//   list:   result = [[省], [市], [区], [街道]]  ← 二维数组,按索引位置区分级别
//   search: result = [[匹配项]]  ← 一维数组(放在 result[0])
//   字段: id / name(简称,如"北京") / fullname(全称,如"北京市")

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
	geoCitiesCacheTTL = 1 * 3600 // 1h:临时缩短,等业务稳定后再改回 7d

	geoCitiesProvincesKey = "local-life:geo:cities:v1:provinces"
	geoCitiesSearchKeyFmt = "local-life:geo:cities:v1:search:%s"

	citiesTencentDistrictURL = "https://apis.map.qq.com/ws/district/v1/"
	citiesTencentFailTTL     = 5 * 60
	citiesTencentFailKey     = "local-life:geo:cities:v1:fail"
	citiesTencentTimeout     = 3 * time.Second
)

type GeoCitiesLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewGeoCitiesLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GeoCitiesLogic {
	return &GeoCitiesLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// GeoCities 入口:keyword 优先
func (l *GeoCitiesLogic) GeoCities(req *types.GeoCitiesReq) (resp *types.GeoCitiesResp, err error) {
	key := l.svcCtx.TencentMapKey
	if !isTencentMapKeyConfigured(key) {
		l.Errorf("[geo_cities] tencent map key not configured")
		return &types.GeoCitiesResp{Items: []types.GeoCityItem{}}, nil
	}

	if req.Keyword != "" {
		return l.search(req.Keyword)
	}
	return l.listProvinces()
}

// search 行政区划搜索
func (l *GeoCitiesLogic) search(keyword string) (*types.GeoCitiesResp, error) {
	limit := 50
	if cached, _ := l.svcCtx.RedisCli.Get(fmt.Sprintf(geoCitiesSearchKeyFmt, keyword)); cached != "" {
		var hit []types.GeoCityItem
		if json.Unmarshal([]byte(cached), &hit) == nil {
			return &types.GeoCitiesResp{Items: hit}, nil
		}
	}

	body, err := l.callTencentSearch(keyword, limit)
	if err != nil {
		l.Errorf("[geo_cities] search failed: kw=%s err=%v", keyword, err)
		return &types.GeoCitiesResp{Items: []types.GeoCityItem{}}, nil
	}

	if data, mErr := json.Marshal(body); mErr == nil {
		_ = l.svcCtx.RedisCli.Setex(fmt.Sprintf(geoCitiesSearchKeyFmt, keyword), string(data), geoCitiesCacheTTL)
	}
	return &types.GeoCitiesResp{Items: body}, nil
}

// listProvinces 全国省级列表
func (l *GeoCitiesLogic) listProvinces() (*types.GeoCitiesResp, error) {
	if cached, _ := l.svcCtx.RedisCli.Get(geoCitiesProvincesKey); cached != "" {
		var hit []types.GeoCityItem
		if json.Unmarshal([]byte(cached), &hit) == nil {
			return &types.GeoCitiesResp{Items: hit}, nil
		}
	}

	items, err := l.callTencentList()
	if err != nil {
		l.Errorf("[geo_cities] list provinces failed: err=%v", err)
		return &types.GeoCitiesResp{Items: []types.GeoCityItem{}}, nil
	}

	if data, mErr := json.Marshal(items); mErr == nil {
		_ = l.svcCtx.RedisCli.Setex(geoCitiesProvincesKey, string(data), geoCitiesCacheTTL)
	}
	return &types.GeoCitiesResp{Items: items}, nil
}

// ============== 腾讯 HTTP 调用 ==============

// tencentCityItem 腾讯 V1 行政区划基本字段(cities 专用)
type tencentCityItem struct {
	ID       string `json:"id"`       // 国标 adcode
	Name     string `json:"name"`     // 简称,如"北京"
	Fullname string `json:"fullname"` // 全称,如"北京市"
}

// tencentCityResponse /list 和 /search 通用响应
type tencentCityResponse struct {
	Status  int               `json:"status"`
	Message string            `json:"message"`
	Result  [][]tencentCityItem `json:"result"`
}

// callTencentSearch /search 接口
func (l *GeoCitiesLogic) callTencentSearch(keyword string, limit int) ([]types.GeoCityItem, error) {
	q := url.Values{}
	q.Set("keyword", keyword)
	q.Set("key", l.svcCtx.TencentMapKey)
	q.Set("page_size", fmt.Sprintf("%d", limit))
	q.Set("page_index", "1")
	u := citiesTencentDistrictURL + "search?" + q.Encode()

	raw, err := l.httpGet(u)
	if err != nil {
		return nil, err
	}
	var resp tencentCityResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}
	if resp.Status != 0 {
		return nil, fmt.Errorf("tencent status=%d msg=%s", resp.Status, resp.Message)
	}

	var items []tencentCityItem
	if len(resp.Result) > 0 {
		items = resp.Result[0]
	}

	out := make([]types.GeoCityItem, 0, len(items))
	for _, it := range items {
		out = append(out, districtToCityItem(it))
	}
	return out, nil
}

// callTencentList /list 接口,只取省级
func (l *GeoCitiesLogic) callTencentList() ([]types.GeoCityItem, error) {
	q := url.Values{}
	q.Set("key", l.svcCtx.TencentMapKey)
	q.Set("page_size", "1000")
	u := citiesTencentDistrictURL + "list?" + q.Encode()

	raw, err := l.httpGet(u)
	if err != nil {
		return nil, err
	}
	var resp tencentCityResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode list response: %w", err)
	}
	if resp.Status != 0 {
		return nil, fmt.Errorf("tencent status=%d msg=%s", resp.Status, resp.Message)
	}

	var provinces []tencentCityItem
	if len(resp.Result) > 0 {
		provinces = resp.Result[0]
	}

	out := make([]types.GeoCityItem, 0, len(provinces))
	for _, p := range provinces {
		out = append(out, districtToCityItem(p))
	}
	sortCitiesByImportance(out)
	return out, nil
}

// districtToCityItem 标准化转换
func districtToCityItem(it tencentCityItem) types.GeoCityItem {
	name := it.Fullname
	if name == "" {
		name = it.Name
	}
	return types.GeoCityItem{
		CityAdcode: it.ID,
		CityName:   name,
	}
}

// sortCitiesByImportance 直辖市+港澳台排前,其他按 adcode
func sortCitiesByImportance(items []types.GeoCityItem) {
	important := map[string]int{
		"110000": 1, // 北京
		"310000": 2, // 上海
		"440100": 3, // 广州
		"440300": 4, // 深圳
		"120000": 5, // 天津
		"500000": 6, // 重庆
		"810000": 7, // 香港
		"820000": 8, // 澳门
		"710000": 9, // 台湾
	}
	sort.SliceStable(items, func(i, j int) bool {
		ip := important[items[i].CityAdcode]
		jp := important[items[j].CityAdcode]
		if ip != jp {
			return ip < jp
		}
		return items[i].CityAdcode < items[j].CityAdcode
	})
}

// httpGet 通用 HTTP GET + 失败标记
func (l *GeoCitiesLogic) httpGet(url string) ([]byte, error) {
	if failHit, _ := l.svcCtx.RedisCli.Get(citiesTencentFailKey); failHit != "" {
		return nil, fmt.Errorf("tencent fail cached")
	}

	client := &http.Client{Timeout: citiesTencentTimeout}
	req, err := http.NewRequestWithContext(l.ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		_ = l.svcCtx.RedisCli.Setex(citiesTencentFailKey, "1", citiesTencentFailTTL)
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// 检查 status code
	var statusCheck struct {
		Status int `json:"status"`
	}
	_ = json.Unmarshal(body, &statusCheck)
	if statusCheck.Status != 0 {
		_ = l.svcCtx.RedisCli.Setex(citiesTencentFailKey, "1", citiesTencentFailTTL)
		return nil, fmt.Errorf("tencent status=%d", statusCheck.Status)
	}
	return body, nil
}