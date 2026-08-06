package transport

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/auth"
	"github.com/jwcen/argent-go/internal/transport/middleware"
)

// New 构建 gin 引擎并挂载所有 transport 层中间件与路由。
//
// Deps 汇总 router 需要的所有外部依赖。
type Deps struct {
	Logger *slog.Logger
	Auth   *auth.Service

	// UserDB 按 userID 返回该用户的独立库句柄（portfolio 等业务域用）。
	// nil 表示不挂载业务域路由。
	UserDB func(userID int64) (*sql.DB, error)

	// Static 是前端静态资源服务，nil 表示纯 API 模式。
	Static *StaticHandler
}

func New(d Deps) *gin.Engine {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}

	r := gin.New()
	r.SetTrustedProxies(nil)

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

		protected := r.Group("/api")
		protected.Use(middleware.RequireAuth(d.Auth))

		if d.UserDB != nil {
			NewPortfolioHandler(d.UserDB).Register(protected)
		}
	}

	if d.Static != nil {
		d.Static.Register(r)
	} else {
		r.NoRoute(func(c *gin.Context) {
			WriteError(c, http.StatusNotFound, "not found")
		})
	}

	return r
}
