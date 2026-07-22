package middleware

import (
	"net/http"
	"strconv"
	"strings"
)

// 跨域相关 HTTP 头常量
const (
	headerOrigin           = "Origin"
	headerAllowOrigin      = "Access-Control-Allow-Origin"
	headerAllowMethods     = "Access-Control-Allow-Methods"
	headerAllowHeaders     = "Access-Control-Allow-Headers"
	headerAllowCredentials = "Access-Control-Allow-Credentials"
	headerExposeHeaders    = "Access-Control-Expose-Headers"
	headerMaxAge           = "Access-Control-Max-Age"
	headerVary             = "Vary"
)

// CorsOptions CORS 配置（运行时使用，不依赖任何上层包）
//   - 由调用方（如 mini.go）从 yaml/config 自行转换
type CorsOptions struct {
	// AllowOrigins 允许的来源列表（精确匹配，不支持通配符）
	// 留空表示拒绝所有跨域（仅同源访问）
	AllowOrigins []string

	// AllowMethods 允许的 HTTP 方法
	AllowMethods []string

	// AllowHeaders 允许的请求头
	AllowHeaders []string

	// ExposeHeaders 暴露给前端的响应头
	ExposeHeaders []string

	// AllowCredentials 是否允许携带凭证（cookie / Authorization）
	// 设为 true 时 AllowOrigins 必须具体 origin，不能用 "*"
	AllowCredentials bool

	// MaxAge 预检请求缓存时间（秒），默认 86400
	MaxAge int
}

// CorsMiddleware 跨域中间件（白名单模式）
//   - 用法: server.WithMiddleware(middleware.CorsMiddleware(opts))
//   - 白名单命中才回写 Access-Control-Allow-Origin
//   - 预检请求（OPTIONS）必须命中白名单才返 204，否则返 403
//   - V1 不支持子域通配符（如 *.example.com），V2 再加
func CorsMiddleware(opts CorsOptions) func(http.HandlerFunc) http.HandlerFunc {
	// 预格式化字符串 + 转 map，避免每次请求重复计算
	allowMethods := strings.Join(opts.AllowMethods, ", ")
	allowHeaders := strings.Join(opts.AllowHeaders, ", ")
	exposeHeaders := strings.Join(opts.ExposeHeaders, ", ")
	maxAge := strconv.Itoa(opts.MaxAge)

	// 将允许的 origin 列表转为 map，提升查找效率
	allowOriginsMap := make(map[string]bool, len(opts.AllowOrigins))
	for _, o := range opts.AllowOrigins {
		allowOriginsMap[o] = true
	}

	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get(headerOrigin)

			// 1. 校验 origin 是否在白名单
			originAllowed := origin != "" && allowOriginsMap[origin]

			// 2. 预检请求必须命中白名单，否则拒绝（防止 CSRF 探测）
			if r.Method == http.MethodOptions {
				if !originAllowed {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				// 命中：设置跨域头 + 返 204
				w.Header().Set(headerAllowOrigin, origin)
				w.Header().Add(headerVary, "Origin")
				w.Header().Set(headerAllowMethods, allowMethods)
				w.Header().Set(headerAllowHeaders, allowHeaders)
				if opts.AllowCredentials {
					w.Header().Set(headerAllowCredentials, "true")
				}
				w.Header().Set(headerMaxAge, maxAge)
				w.WriteHeader(http.StatusNoContent)
				return
			}

			// 3. 实际请求：仅命中白名单才回写跨域头
			if originAllowed {
				w.Header().Set(headerAllowOrigin, origin)
				w.Header().Add(headerVary, "Origin")
				w.Header().Set(headerExposeHeaders, exposeHeaders)
				if opts.AllowCredentials {
					w.Header().Set(headerAllowCredentials, "true")
				}
			}

			// 4. 继续处理实际请求
			next(w, r)
		}
	}
}
