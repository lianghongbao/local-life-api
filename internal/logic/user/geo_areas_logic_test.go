package user

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/zeromicro/go-zero/core/stores/redis"

	"local-life-api/internal/svc"
	"local-life-api/internal/types"
)

// TestAreasLogic_空city_keyword返回空 验证两个入参都为空时短路返回空列表(不打 DB 不打 cache)
func TestAreasLogic_空city_keyword返回空(t *testing.T) {
	s := miniredis.RunT(t)
	rds, err := redis.NewRedis(redis.RedisConf{Host: s.Addr(), Type: "node"})
	assert.NoError(t, err)
	defer s.Close()

	svcCtx := &svc.ServiceContext{
		RedisCli:      rds,
		TencentMapKey: "",
	}

	l := NewGeoAreasLogic(context.Background(), svcCtx)
	resp, err := l.GeoAreas(&types.GeoAreasReq{CityAdcode: "", Keyword: ""})

	assert.NoError(t, err, "空入参应走短路,不返回 error")
	assert.NotNil(t, resp, "resp 不应为 nil(避免前端 nil deref)")
	assert.Empty(t, resp.Items, "空入参应返回空 Items")
}

// TestStreetsLogic_空area_adcode返回空 同上,验证 streets 端点的空入参短路。
func TestStreetsLogic_空area_adcode返回空(t *testing.T) {
	s := miniredis.RunT(t)
	rds, err := redis.NewRedis(redis.RedisConf{Host: s.Addr(), Type: "node"})
	assert.NoError(t, err)
	defer s.Close()

	svcCtx := &svc.ServiceContext{
		RedisCli:      rds,
		TencentMapKey: "",
	}

	l := NewGeoStreetsLogic(context.Background(), svcCtx)
	resp, err := l.GeoStreets(&types.GeoStreetsReq{AreaAdcode: ""})

	assert.NoError(t, err, "空 adcode 应走短路,不返回 error")
	assert.NotNil(t, resp, "resp 不应为 nil(避免前端 nil deref)")
	assert.Empty(t, resp.Items, "空入参应返回空 Items")
}