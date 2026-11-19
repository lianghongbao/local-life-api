// 同城推荐:reverse geocoding + Redis 30d 缓存

package user

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"

	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

const (
	// geoReverseCacheTTL 命中缓存 30 天(同坐标块 reverse 结果长期稳定)
	geoReverseCacheTTL = 30 * 24 * 3600

	// geoReverseCacheKeyFmt 坐标按 0.01°(≈1km)取整的 cache key 模板
	geoReverseCacheKeyFmt = "local-life:geo:reverse:v1:%d:%d"

	// tencentReverseURL 腾讯位置服务 reverse geocoding 接口
	tencentReverseURL = "https://apis.map.qq.com/ws/geocoder/v1/"

	// tencentFailKeyFmt 腾讯调用失败标记的 cache key(防雪崩)
	tencentFailKeyFmt = "local-life:geo:reverse:v1:fail:%d:%d"

	// tencentFailTTL 失败标记 TTL(5 分钟),后续请求直接走降级
	tencentFailTTL = 5 * 60

	// tencentTimeout 腾讯 HTTP 调用超时
	tencentTimeout = 3 * time.Second
)

type GeoReverseLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewGeoReverseLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GeoReverseLogic {
	return &GeoReverseLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// geoReverseCacheKey 坐标按 0.01°(≈1km)取整,与 wx.getFuzzyLocation 误差量级匹配
//   同一模糊点(0.01° 内)共享一个 cache key,避免重复打腾讯
func geoReverseCacheKey(lat, lng float64) string {
	return fmt.Sprintf(geoReverseCacheKeyFmt,
		int64(math.Round(lat*100)), int64(math.Round(lng*100)))
}

// tencentFailKey 失败标记 key(同坐标块的失败去重)
func tencentFailKey(lat, lng float64) string {
	return fmt.Sprintf(tencentFailKeyFmt,
		int64(math.Round(lat*100)), int64(math.Round(lng*100)))
}

// GeoReverse 1. 查 Redis 缓存 2. miss 则调腾讯 reverse 3. 写回 Redis
//   失败(腾讯异常/未配置 key/命中失败标记)返回空字段,由前端走"全市推荐"降级
//   lat/lng 为 0 时直接返回空(避免 0,0 命中大西洋)
func (l *GeoReverseLogic) GeoReverse(req *types.GeoReverseReq) (resp *types.GeoReverseResp, err error) {
	if req.Lat == 0 || req.Lng == 0 {
		return &types.GeoReverseResp{}, nil
	}

	key := geoReverseCacheKey(req.Lat, req.Lng)

	// 1. fail 标记防雪崩(同一坐标块 5 分钟内只打一次腾讯)
	if failHit, _ := l.svcCtx.RedisCli.Get(tencentFailKey(req.Lat, req.Lng)); failHit != "" {
		l.Infof("[geo_reverse] hit fail cache, key=%s", key)
		return &types.GeoReverseResp{}, nil
	}

	// 2. 查正常缓存
	if cached, cacheErr := l.svcCtx.RedisCli.Get(key); cacheErr == nil && cached != "" {
		var hit types.GeoReverseResp
		if err := json.Unmarshal([]byte(cached), &hit); err == nil {
			return &hit, nil
		}
		l.Errorf("[geo_reverse] cache unmarshal failed: err=%v", err)
	}

	// 3. 调腾讯 reverse(未配置 key / 占位符 → 降级)
	if !isTencentMapKeyConfigured(l.svcCtx.TencentMapKey) {
		l.Errorf("[geo_reverse] tencent map key not configured")
		return &types.GeoReverseResp{}, nil
	}

	tencentResp, callErr := l.callTencentReverse(req.Lat, req.Lng)
	if callErr != nil {
		l.Errorf("[geo_reverse] tencent api call failed: err=%v", callErr)
		// 只对真正的网络/HTTP 错误写 fail 标记(JSON 解析失败已由 flexAddr 消除)。
		// 腾讯正常返回但字段全空(如海域坐标)不能写 fail 标记,否则 5 分钟内全部降级。
		_ = l.svcCtx.RedisCli.Setex(tencentFailKey(req.Lat, req.Lng), "1", tencentFailTTL)
		return &types.GeoReverseResp{}, nil
	}

	resp = tencentResp

	// 4a. 腾讯返回成功但 4 字段全空(海域/直辖市边界/未匹配坐标) → 记 WARN
	//     不写 fail 缓存(避免后续请求被 5 分钟降级拖累),但要留痕便于排查脏数据
	if resp.CityAdcode == "" && resp.AreaAdcode == "" &&
		resp.CityName == "" && resp.AreaName == "" && resp.StreetName == "" {
		l.Infof("[geo_reverse] WARN tencent empty body: lat=%f, lng=%f, formatted=%q",
			req.Lat, req.Lng, resp.FormattedAddress)
	}

	// 4. 写缓存。空结果(无 city 且无 area)不缓存,避免海域/无匹配坐标
	//    把空字段缓存 30 天导致整个小区永远降级。
	if resp.CityAdcode != "" || resp.AreaAdcode != "" {
		if data, mErr := json.Marshal(resp); mErr == nil {
			if setErr := l.svcCtx.RedisCli.Setex(key, string(data), geoReverseCacheTTL); setErr != nil {
				l.Errorf("[geo_reverse] cache write failed: err=%v", setErr)
			}
		}
	}

	return resp, nil
}

// tencentReverseBody 腾讯 reverse geocoding 响应结构(只取业务关心的字段)
type tencentReverseBody struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
	Result  struct {
		AddressComponent struct {
			City     flexAddr `json:"city"`
			District flexAddr `json:"district"`
			Street   flexAddr `json:"street"`
		} `json:"address_component"`
		// AdInfo 腾讯行政区划代码信息:
		//   - adcode  6 位区/县级 adcode(深圳/珠海等市辖区都有)
		//   - city_code 9 位带国家码的市级 adcode(如 156440400 → 市 adcode = 440400)
		// 用途:address_component.city/district 经常是字符串(无 adcode),
		//       这里兜底从 ad_info 取真实 adcode
		AdInfo struct {
			Adcode   string `json:"adcode"`    // 区 adcode(6 位)
			CityCode string `json:"city_code"` // 市 adcode(9 位,前 3 位是国家码)
		} `json:"ad_info"`
		FormattedAddresses struct {
			Recommend string `json:"recommend"`
		} `json:"formatted_addresses"`
	} `json:"result"`
}

// flexAddr 容忍腾讯 reverse geocoding 中 city/district/street 的两种 JSON 形态:
//   1. 对象:{"adcode":"510100","name":"成都市"}    — 大陆正常坐标
//   2. 字符串:"香港特别行政区" / "[]" / ""        — 香港/澳门/直辖市/无匹配
// 旧版本死锁要求对象形式,遇到字符串会抛 `cannot unmarshal string into ...`
// 并把整个 result 丢掉,刷 error 日志;新版本任一形态都能解,空值时降级为空字段。
type flexAddr struct {
	Adcode string `json:"adcode,omitempty"`
	Name   string `json:"name,omitempty"`
}

// flexAddrAlias 临时 alias 用于递归解对象(避免调到自己的 UnmarshalJSON)
type flexAddrAlias flexAddr

func (f *flexAddr) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	// 对象形式:首字符 '{' → 按 alias 解(不递归)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var alias flexAddrAlias
		if err := json.Unmarshal(data, &alias); err != nil {
			return err
		}
		*f = flexAddr(alias)
		return nil
	}
	// 字符串形式:"香港特别行政区" / "[]" / "" — 只把真实名写 Name,占位串当空
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if s != "" && s != "[]" {
		f.Name = s
	}
	return nil
}

// callTencentReverse 调用腾讯 reverse geocoding 接口
//   返回值:省市区/街道/格式化地址 + adcode,失败返回 error
func (l *GeoReverseLogic) callTencentReverse(lat, lng float64) (*types.GeoReverseResp, error) {
	u, _ := url.Parse(tencentReverseURL)
	q := u.Query()
	q.Set("location", fmt.Sprintf("%f,%f", lat, lng))
	q.Set("key", l.svcCtx.TencentMapKey)
	q.Set("get_poi", "0")
	u.RawQuery = q.Encode()

	// 用 NewRequestWithContext 把 l.ctx 注入请求:客户端断开/超时可立即取消 goroutine
	//   (若用 client.Get,只能等 tencentTimeout 3s 才返回)
	req, err := http.NewRequestWithContext(l.ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	// 腾讯位置服务 WebServiceAPI 用 Referer 白名单校验来源:
	//   裸调(无 Referer)→ status=110 来源域名未被授权
	//   带 https://www.lamborai.com Referer → 正常返回
	// 后端 HTTP 调用没有真正的浏览器 Referer,这里用一个配在白名单里的域名"伪装"。
	// 推荐用小程序域名 servicewechat.com,跟 C 端 SDK 一致便于统一。
	req.Header.Set("Referer", "https://servicewechat.com")
	client := &http.Client{Timeout: tencentTimeout}
	httpResp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	var body tencentReverseBody
	if err := json.NewDecoder(httpResp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.Status != 0 {
		return nil, fmt.Errorf("tencent status=%d msg=%s", body.Status, body.Message)
	}

	r := body.Result
	// 街道名 = 区名 时(无街道级数据),不输出冗余字段
	streetName := ""
	if r.AddressComponent.Street.Name != r.AddressComponent.District.Name {
		streetName = r.AddressComponent.Street.Name
	}

	// adcode 兜底从 ad_info 取:
	//   - address_component.city/district 经常是字符串"name"形式,没有 adcode 字段
	//   - 但 ad_info.adcode (区 6 位) + ad_info.city_code (市 9 位带国家码) 永远有
	// city_code 形如 "156440400": 前 3 位是国家码 156 (中国),后 6 位就是市 adcode
	cityAdcode := r.AddressComponent.City.Adcode
	if cityAdcode == "" && len(r.AdInfo.CityCode) >= 6 {
		cityAdcode = r.AdInfo.CityCode[len(r.AdInfo.CityCode)-6:]
	}
	areaAdcode := r.AddressComponent.District.Adcode
	if areaAdcode == "" {
		areaAdcode = r.AdInfo.Adcode
	}

	return &types.GeoReverseResp{
		CityAdcode:       cityAdcode,
		CityName:         r.AddressComponent.City.Name,
		AreaAdcode:       areaAdcode,
		AreaName:         r.AddressComponent.District.Name,
		StreetName:       streetName,
		FormattedAddress: r.FormattedAddresses.Recommend,
	}, nil
}

// ResolveGeoFields 给定 lat/lng 调用腾讯 reverse(走 Redis 缓存),返回写入 DB 的 5 字段。
//   - 0,0 坐标 / 腾讯 key 未配 / 调用失败 → 5 字段全空字符串,err=nil(不阻塞主流程)
//   - 主链路只需 if err == nil 直接读返回值写库,无需关心 reverse 内部细节
//   - post_create / post_update / shop_create / shop_update 4 处统一调用,
//     避免重复 NewGeoReverseLogic().GeoReverse() 模板代码
//
// 返回值顺序刻意对齐 model.PostInfo / model.Shop 的字段顺序,调用方一眼能对齐。
func ResolveGeoFields(ctx context.Context, svcCtx *svc.ServiceContext, lat, lng float64) (cityAdcode, cityName, areaAdcode, areaName, streetName string) {
	if lat == 0 || lng == 0 {
		return "", "", "", "", ""
	}
	rev, revErr := NewGeoReverseLogic(ctx, svcCtx).GeoReverse(&types.GeoReverseReq{
		Lat: lat,
		Lng: lng,
	})
	if revErr != nil || rev == nil {
		return "", "", "", "", ""
	}
	return rev.CityAdcode, rev.CityName, rev.AreaAdcode, rev.AreaName, rev.StreetName
}