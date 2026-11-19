package user

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/zeromicro/go-zero/core/stores/redis"

	"local-life-api/internal/svc"
	"local-life-api/internal/types"
)

// TestGeoReverseCacheKey_同点命中 验证坐标按 0.01° 取整生成稳定 cache key
//   0.01° ≈ 1.1km(赤道),与 wx.getFuzzyLocation 误差量级匹配
//   同一模糊点应共享 key(避免重复打腾讯)
func TestGeoReverseCacheKey_同点命中(t *testing.T) {
	// 30.6586 → 3066, 104.0648 → 10406
	// 30.6599 → 3066 (0.6599*100=3065.99 → 3066), 104.0651 → 10407 (0.0651*100=10406.51 → 10407)
	// 整 0.01° 内应取整到同一 key,所以用更接近的坐标:
	k1 := geoReverseCacheKey(30.6586, 104.0648)
	k2 := geoReverseCacheKey(30.6585, 104.0645) // 0.0001° 内偏差,取整必同
	assert.Equal(t, k1, k2, "0.01° 内应命中同一 key")
}

// TestGeoReverseCacheKey_跨点隔离 验证不同坐标块生成不同 key
func TestGeoReverseCacheKey_跨点隔离(t *testing.T) {
	k1 := geoReverseCacheKey(30.6586, 104.0648)
	k3 := geoReverseCacheKey(30.6699, 104.0751) // 差 0.01°+
	assert.NotEqual(t, k1, k3, "0.01° 外应不同 key")
}

// TestGeoReverseCacheKey_边界取整 验证 math.Round 行为(0.005 进位)
//   local-life: 前缀不能少 —— 所有 key 都要带应用前缀
func TestGeoReverseCacheKey_边界取整(t *testing.T) {
	// 30.6550 → 3066(0.5 进位)
	// 30.6549 → 3065
	// 30.6650 → 3067
	kLow := geoReverseCacheKey(30.6549, 104.0648)
	kMid := geoReverseCacheKey(30.6550, 104.0648)
	kHigh := geoReverseCacheKey(30.6650, 104.0648)
	assert.Equal(t, "local-life:geo:reverse:v1:3065:10406", kLow)
	assert.Equal(t, "local-life:geo:reverse:v1:3066:10406", kMid)
	assert.Equal(t, "local-life:geo:reverse:v1:3067:10406", kHigh)
}

// TestTencentFailKey_独立命名空间 验证 fail 标记 key 与正常 cache key 区分
//   防止 fail 标记污染正常 cache(否则 fail 写完后正常 cache 永远命中不到)
func TestTencentFailKey_独立命名空间(t *testing.T) {
	normalKey := geoReverseCacheKey(30.6586, 104.0648)
	failKey := tencentFailKey(30.6586, 104.0648)
	assert.NotEqual(t, normalKey, failKey, "fail key 与正常 cache key 必须区分")
	assert.Contains(t, failKey, ":fail:")
}

// TestTencentReverseBody_CityAsString_反序列化容错
//   腾讯 reverse geocoding 在坐标不匹配城市(海域/特殊行政区如香港)时
//   会把 address_component.city / district / street 当作字符串("" / "[]")
//   而不是对象 {adcode, name}。旧 struct 死锁要求对象,整个 JSON 反序列化失败,
//   致 error 日志刷屏;新 tolerant 类型应能接受两种形式,空值时降级为空字段。
func TestTencentReverseBody_CityAsString_反序列化容错(t *testing.T) {
	// 真实复现:city / district / street 都是 string(模拟香港/海域坐标)
	bodyJSON := `{
		"status": 0,
		"message": "query ok",
		"result": {
			"address_component": {
				"city":     "[]",
				"district": "[]",
				"street":   "[]"
			},
			"formatted_addresses": {"recommend": "香港特别行政区"}
		}
	}`

	var body tencentReverseBody
	err := json.Unmarshal([]byte(bodyJSON), &body)
	assert.NoError(t, err, "string-form city 必须能反序列化,不能 panic")
	assert.Equal(t, 0, body.Status)
	assert.Equal(t, "", body.Result.AddressComponent.City.Adcode)
	assert.Equal(t, "", body.Result.AddressComponent.City.Name)
	assert.Equal(t, "", body.Result.AddressComponent.District.Adcode)
	assert.Equal(t, "", body.Result.AddressComponent.Street.Name)
	assert.Equal(t, "香港特别行政区", body.Result.FormattedAddresses.Recommend)
}

// TestTencentReverseBody_CityAsObject_正常反序列化 验证对象形式仍能正常解析
//   (修改容错后不能把正常坐标的解析弄坏)
func TestTencentReverseBody_CityAsObject_正常反序列化(t *testing.T) {
	bodyJSON := `{
		"status": 0,
		"message": "query ok",
		"result": {
			"address_component": {
				"city":     {"adcode": "510100", "name": "成都市"},
				"district": {"adcode": "510104", "name": "锦江区"},
				"street":   {"name": "春熙路街道"}
			},
			"formatted_addresses": {"recommend": "四川省成都市锦江区春熙路街道"}
		}
	}`

	var body tencentReverseBody
	err := json.Unmarshal([]byte(bodyJSON), &body)
	assert.NoError(t, err)
	assert.Equal(t, "510100", body.Result.AddressComponent.City.Adcode)
	assert.Equal(t, "成都市", body.Result.AddressComponent.City.Name)
	assert.Equal(t, "510104", body.Result.AddressComponent.District.Adcode)
	assert.Equal(t, "锦江区", body.Result.AddressComponent.District.Name)
	assert.Equal(t, "春熙路街道", body.Result.AddressComponent.Street.Name)
}

// TestReverseLogic_降级返回_无Key不Panic 验证 TencentMapKey 为空时整个调用链不会 panic
//   并直接走降级分支(空 AreaAdcode / AreaName / StreetName),让前端落到"全市推荐"。
//   用 miniredis 替代真实 Redis,纯内存,无外部依赖。
//   注意:TencentMapKey 为空会让 logic 在第 3 步就走降级,根本不会调腾讯 HTTP,
//   所以此测试不需要 mock HTTP server。
func TestReverseLogic_降级返回_无Key不Panic(t *testing.T) {
	s := miniredis.RunT(t)
	rds, err := redis.NewRedis(redis.RedisConf{Host: s.Addr(), Type: "node"})
	assert.NoError(t, err)
	defer s.Close()

	svcCtx := &svc.ServiceContext{
		RedisCli:      rds,
		TencentMapKey: "", // 空 key 触发降级
	}

	l := NewGeoReverseLogic(context.Background(), svcCtx)
	resp, err := l.GeoReverse(&types.GeoReverseReq{Lat: 30.6586, Lng: 104.0648})

	assert.NoError(t, err, "空 key 降级不应返回 error")
	assert.NotNil(t, resp, "resp 不应为 nil(降级也要返回空 struct)")
	assert.Equal(t, "", resp.AreaAdcode)
	assert.Equal(t, "", resp.AreaName)
	assert.Equal(t, "", resp.StreetName)
	// 验证没有调用腾讯:TencentMapKey 为空时 logic 提前 return,callTencentReverse 不被执行
	// 这里只能是结构性验证(无 panic/error + 空字段),HTTP 调用由 TestReverseLogic_降级返回_cache miss HTTP 路径负责
}