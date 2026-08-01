package transport

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/auth"
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
//
// Deps 汇总 router 需要的所有外部依赖。
//
// 用一个结构体而不是长参数列表：后续 Stage 每加一个业务域就多一个 service，
// 参数列表会迅速失控，而结构体字段可以按需增删且调用处保持可读。
type Deps struct {
	Logger *slog.Logger
	Auth   *auth.Service
}

func New(d Deps) *gin.Engine {
	logger := d.Logger
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

	if d.Auth != nil {
		NewAuthHandler(d.Auth).Register(r)

		// 受保护分组：后续业务域 handler 都挂到这里，
		// 一次性获得「必须登录 + 用户已注入 context」的保证。
		protected := r.Group("/api")
		protected.Use(middleware.RequireAuth(d.Auth))
		_ = protected // Stage 3+ 开始往里挂 portfolio / market / assets 等
	}

	return r
}
