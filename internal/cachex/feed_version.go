// Package cachex 提供 Redis 缓存工具函数 —— 首页混合流 Version 号失效机制
//
// 首页缓存 key 格式: home:feed:v1:{scope}:{adcode}:ver{N}:p{page}:s{size}
//   - version key 不设 TTL:若比首页 cache TTL 先过期,会出现"GET version 拿到 0 → 缓存 key 退回 ver0 → 命中 ver0 旧缓存"的逻辑漏洞
package cachex

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

// feed:ver:{scope}:{adcode}  scope ∈ {"city", "area"}
const feedVerKeyTemplate = "feed:ver:%s:%s"

// 进程内 version 缓存 TTL,避免每次首页 query 多一次 Redis GET
const versionLocalCacheTTL = 5 * time.Second

var versionLocalCache sync.Map // key: "scope:adcode"

type versionEntry struct {
	version  int64
	expireAt time.Time
}

// FeedVersionKey 拼接 (scope, adcode) 桶的 version Redis key
//   - scope: 过滤维度,"city" 按城市、"area" 按区
//   - adcode: 6 位国标 adcode 字符串
func FeedVersionKey(scope, adcode string) string {
	return fmt.Sprintf(feedVerKeyTemplate, scope, adcode)
}

// InvalidateFeedVersion INCR version counter 让首页缓存失效
//   - ctx: 请求上下文,失败时用于 logx 输出
//   - rds: go-zero Redis 客户端,执行 INCR
//   - scopes: 要失效的维度列表,如 {"city","area"}
//   - adcodes: 要失效的 adcode 列表,空字符串会被跳过
func InvalidateFeedVersion(ctx context.Context, rds *redis.Redis, scopes []string, adcodes []string) {
	logger := logx.WithContext(ctx)
	for _, scope := range scopes {
		for _, adcode := range adcodes {
			if adcode == "" {
				continue
			}
			key := FeedVersionKey(scope, adcode)
			if _, err := rds.Incr(key); err != nil {
				logger.Errorf("INCR feed version 失败: key=%s, err=%v", key, err)
				continue
			}
			// 清掉本进程对该桶的本地缓存,确保下次 GetFeedVersion 走 Redis
			versionLocalCache.Delete(scope + ":" + adcode)
		}
	}
}

// GetFeedVersion 读取当前 (scope, adcode) 桶的 version,首页缓存 key 拼接用
//   - ctx: 请求上下文,Redis 调用透传
//   - rds: go-zero Redis 客户端
//   - scope: 过滤维度,"city" 或 "area"
//   - adcode: 6 位国标 adcode;空字符串直接返回 0(首页不缓存空 adcode 的桶)
func GetFeedVersion(ctx context.Context, rds *redis.Redis, scope, adcode string) int64 {
	if adcode == "" {
		return 0
	}
	cacheKey := scope + ":" + adcode

	if v, ok := versionLocalCache.Load(cacheKey); ok {
		entry := v.(versionEntry)
		if time.Now().Before(entry.expireAt) {
			return entry.version
		}
	}

	var version int64
	val, err := rds.Get(FeedVersionKey(scope, adcode))
	if err == nil && val != "" {
		version, _ = strconv.ParseInt(val, 10, 64)
	}

	versionLocalCache.Store(cacheKey, versionEntry{
		version:  version,
		expireAt: time.Now().Add(versionLocalCacheTTL),
	})

	return version
}

// UniqueNonEmpty 字符串数组去重 + 过滤空串
//   - strs: 可变参数,任意数量的字符串
//   - 返回: 去掉空串和重复项的有序切片(保序 + 去重)
func UniqueNonEmpty(strs ...string) []string {
	seen := make(map[string]struct{}, len(strs))
	out := make([]string, 0, len(strs))
	for _, s := range strs {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}