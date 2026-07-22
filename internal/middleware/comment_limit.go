package middleware

import (
	"context"
	"net/http"

	"local-life-api/common/jwtx"
	"local-life-api/internal/config"

	"github.com/zeromicro/go-zero/core/limit"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/rest/httpx"
)

// CommentRateLimit 通用评论限流中间件
//   - kind: "create" / "like" / "report"
//   - c:   配置(含 CommentLimit)
//   - r:   redis 实例(分布式限流,PeriodLimit 用 redis Lua 脚本原子计数)
//   - 限流策略:
//     create : 每分钟 c.CommentLimit.CreatePerMin 条
//     like   : 每分钟 c.CommentLimit.LikePerMin 条
//     report : 每小时 c.CommentLimit.ReportPerHour 条
//   - 限流粒度: 按登录用户 openid(与 comment.author_id 同源)
//   - 降级策略:
//     未登录(openid 为空) → 放行,交给 JWT 中间件返 401
//     redis 异常          → 放行 + 日志,避免 redis 抖动杀掉整条评论链路
//     超额                → 429 Too Many Requests
func CommentRateLimit(kind string, c config.Config, r *redis.Redis) func(http.HandlerFunc) http.HandlerFunc {
	var (
		period int
		quota  int
	)
	switch kind {
	case "create":
		period, quota = 60, c.CommentLimit.CreatePerMin
	case "like":
		period, quota = 60, c.CommentLimit.LikePerMin
	case "report":
		period, quota = 3600, c.CommentLimit.ReportPerHour
	default:
		period, quota = 60, 10
	}
	limiter := limit.NewPeriodLimit(period, quota, r, "comment-limit:"+kind)
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			uid := extractUIDFromCtx(req.Context())
			if uid == "" {
				// 未登录用户不限制(由 JWT/auth 中间件处理 401);中间件放行
				next.ServeHTTP(w, req)
				return
			}
			code, err := limiter.TakeCtx(req.Context(), uid)
			if err != nil {
				// 限流服务异常:放行 + log(避免 Redis 挂掉杀掉整条评论链路)
				logx.WithContext(req.Context()).Errorf(
					"comment rate limit take failed, kind=%s uid=%s err=%v", kind, uid, err)
				next.ServeHTTP(w, req)
				return
			}
			if code == limit.OverQuota {
				httpx.Error(w, &tooManyRequestsError{})
				return
			}
			next.ServeHTTP(w, req)
		}
	}
}

// extractUIDFromCtx 从 ctx 取当前登录用户的 openid
//   - 复用项目既有 jwtx.FromContext(与所有 comment logic 同源,保证 key 一致)
//   - jwtx 是叶子包(只依赖 stdlib + golang-jwt),不会与 middleware 形成循环 import
//   - 未登录 / 解析失败 → 返回 ""(调用方放行,交给 JWT 中间件处理 401)
func extractUIDFromCtx(ctx context.Context) string {
	openid, err := jwtx.GetOpenid(ctx)
	if err != nil {
		return ""
	}
	return openid
}

// tooManyRequestsError 让 httpx.Error 输出 429 状态码
type tooManyRequestsError struct{}

func (e *tooManyRequestsError) Error() string { return "rate limited" }

func (e *tooManyRequestsError) StatusCode() int { return http.StatusTooManyRequests }
