package main

import (
	"flag"
	"fmt"

	"local-life-api/common/middleware"
	"local-life-api/internal/background"
	"local-life-api/internal/config"
	"local-life-api/internal/handler"
	"local-life-api/internal/svc"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/mini-api.yaml", "the config file")

// withDefault 若 s 为空则返回 def，用于把 yaml 零值替换为运行时默认值
func withDefault(s []string, def []string) []string {
	if len(s) == 0 {
		return def
	}
	return s
}

// withDefaultInt 若 v 为 0 则返回 def
func withDefaultInt(v int, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func main() {
	flag.Parse()

	var c config.Config
	// UseEnv(): yaml 里的 ${VAR} 自动替换为环境变量值
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	// 按配置决定是否挂 CORS 中间件
	server := rest.MustNewServer(c.RestConf)
	if c.Cors.Enabled {
		opts := middleware.CorsOptions{
			AllowOrigins:     c.Cors.AllowOrigins,
			AllowMethods:     withDefault(c.Cors.AllowMethods, []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"}),
			AllowHeaders:     withDefault(c.Cors.AllowHeaders, []string{"Origin", "Content-Type", "Authorization", "X-Requested-With"}),
			ExposeHeaders:    c.Cors.ExposeHeaders,
			AllowCredentials: c.Cors.AllowCredentials,
			MaxAge:           withDefaultInt(c.Cors.MaxAge, 86400),
		}
		server.Use(middleware.CorsMiddleware(opts))
	}
	defer server.Stop()

	ctx := svc.NewServiceContext(c)

	// 启动后台任务（view_count 落库 flusher）
	stopFlusher := background.StartViewCountFlusher(ctx)
	defer stopFlusher()

	handler.RegisterHandlers(server, ctx)

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
