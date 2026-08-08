package transport

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/auth"
	"github.com/jwcen/argent-go/internal/market"
	"github.com/jwcen/argent-go/internal/transport/middleware"
	"github.com/jwcen/argent-go/internal/transport/ws"
)

// New 构建 gin 引擎并挂载所有 transport 层中间件与路由。
//
// Deps 汇总 router 需要的所有外部依赖。
type Deps struct {
	Logger *slog.Logger
	Auth   *auth.Service

	UserDB func(userID int64) (*sql.DB, error)

	// Market 行情数据源 handler（/api/market/*）。nil 不挂载。
	Market *MarketHandler

	// Kline 日 K 线数据源，供净值曲线叠加沪深300基准；nil 不叠加。
	Kline market.KlineProvider

	// Agent LLM 问股 handler（/api/ask/*）。nil 不挂载。
	Agent *AgentHandler

	// WSHub WebSocket 推送 hub，nil 不挂载 /ws。
	WSHub *ws.Hub

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
			NewPortfolioHandler(d.UserDB, d.Kline).Register(protected)
			NewExternalHandler(d.UserDB).Register(protected)
			NewDataHandler(d.UserDB).Register(protected)
		}
		if d.Market != nil {
			d.Market.Register(protected)
		}
		if d.Agent != nil {
			d.Agent.Register(protected)
		}
		if d.WSHub != nil {
			protected.GET("/ws", d.WSHub.HandleWS)
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
