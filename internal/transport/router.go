package transport

import (
	"log/slog"
	"net/http"

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

	// Static 是前端静态资源服务，nil 表示纯 API 模式（不托管前端）。
	Static *StaticHandler
}

func New(d Deps) *gin.Engine {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}

	r := gin.New()
	r.SetTrustedProxies(nil)

	// gin 默认对「路径存在但方法不对」也返回 404，开启后才会返回 405。
	// 配合下面的 NoMethod，保证错误体格式始终是 {"detail": ...}。
	r.HandleMethodNotAllowed = true
	r.NoMethod(func(c *gin.Context) {
		WriteError(c, http.StatusMethodNotAllowed, "method not allowed")
	})

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
		_ = protected // Stage 4+ 开始往里挂 portfolio / market / assets 等
	}

	// 静态服务必须最后注册：它走 NoRoute 兜底，
	// 语义上就是「以上路由都没匹配到，才去静态资源里找」。
	if d.Static != nil {
		d.Static.Register(r)
	} else {
		// 纯 API 模式下也要保证 404 是 JSON，而不是 gin 默认的空 body。
		r.NoRoute(func(c *gin.Context) {
			WriteError(c, http.StatusNotFound, "not found")
		})
	}

	return r
}
