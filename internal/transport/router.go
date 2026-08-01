package transport

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/transport/middleware"
)

// New 构建 gin 引擎并挂载所有 transport 层中间件与路由。
//
// 关键改动（相比 Stage 0 的 gin.Default()）：
//   - 用 gin.New() 而不是 gin.Default()，去掉 gin 内置的 Logger/Recovery 中间件；
//   - 改用基于 slog 的 RequestLogger / Recovery，保证全链路只有一条结构化日志，
//     且每条都自动带 request_id（由 middleware.RequestID + log.ctxHandler 协作）。
//   - SetTrustedProxies(nil) 表示不信任任何代理，消除 “trusted all proxies” 告警，
//     ClientIP() 退化为直接使用 RemoteAddr。
func New(logger *slog.Logger) *gin.Engine {
	if logger == nil {
		logger = slog.Default()
	}

	r := gin.New()
	r.SetTrustedProxies(nil)

	r.Use(middleware.RequestID())
	r.Use(middleware.Recovery(logger))
	r.Use(middleware.RequestLogger(logger))

	hh := &HealthHandler{}
	hh.Register(r)

	return r
}
