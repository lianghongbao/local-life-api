// Package keys 定义本应用的 Redis key 前缀。
//
// 共享底座上 Redis 是多个应用共用的（chatroom 也连同一个实例），
// 隔离靠 key 前缀 —— 本应用的所有 key 都带 local-life: 前缀，
// chatroom 的带 chatroom: 前缀，两边不会撞。
//
// 全应用只有这一处定义前缀。新增 key 时从这里取，不要在各处散着写字面量。
//
// 注意 go-zero 的 cache.Option 不支持 key 前缀（只有 WithExpiry /
// WithNotFoundExpiry），cacheNode 把 key 原样透传给 Redis；
// goctl 生成的 model 里 key 是内联 fmt.Sprintf 拼的，也没有拦截点。
// 所以前缀只能落在 key 的构造处 —— 生成文件里的 const 值是手改的，
// `goctl model` 重新生成后要重新打补丁（见 internal/model/README.md）。
package keys

import "strings"

// Prefix 是本应用所有 Redis key 的统一前缀。
//
// 结尾的 ":" 是分隔符 —— 拼接时直接 Prefix + 业务 key，
// 不要在业务 key 里再带分隔符。
const Prefix = "local-life:"

// Key 给业务 key 加上应用前缀。
//
// key 是应用内的完整 key（不含前缀），比如 "cache:user:id:123"。
// 已经带前缀的原样返回 —— 避免重复加前缀。
func Key(key string) string {
	if strings.HasPrefix(key, Prefix) {
		return key
	}
	return Prefix + key
}

// PrefixOf 取 key 的应用前缀部分。用于反查 key 属于哪个应用。
func PrefixOf(key string) string {
	if !strings.HasPrefix(key, Prefix) {
		return ""
	}
	return Prefix
}
