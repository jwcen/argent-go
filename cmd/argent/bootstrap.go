package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/agent"
	"github.com/jwcen/argent-go/internal/auth"
	"github.com/jwcen/argent-go/internal/infra/log"
	"github.com/jwcen/argent-go/internal/infra/sqlite"
	"github.com/jwcen/argent-go/internal/job"
	"github.com/jwcen/argent-go/internal/market"
	"github.com/jwcen/argent-go/internal/store"
	"github.com/jwcen/argent-go/internal/transport"
	"github.com/jwcen/argent-go/internal/transport/ws"
	"github.com/jwcen/argent-go/web"
)

type App struct {
	Engine    *gin.Engine
	Store     *store.Manager
	Scheduler *job.Scheduler
	WSHub     *ws.Hub
}

func (a *App) Close() error {
	if a.Scheduler != nil {
		a.Scheduler.Stop()
	}
	if a.WSHub != nil {
		a.WSHub.Stop()
	}
	if a.Store != nil {
		return a.Store.Close()
	}
	return nil
}

func Build(ctx context.Context) (*App, error) {
	logger := buildLogger()

	mgr, err := store.Open(ctx, store.Config{
		DataDir:  envOr("ARGENT_DATA_DIR", "./data"),
		LegacyDB: os.Getenv("ARGENT_LEGACY_DB"),
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: open store: %w", err)
	}

	authRepo := sqlite.NewAuthRepo(mgr.Global())
	authSvc := auth.NewService(
		authRepo,
		auth.NewLogMailer(logger),
		mgr,
		auth.DefaultConfig(),
	)

	static, err := buildStatic(logger)
	if err != nil {
		_ = mgr.Close()
		return nil, fmt.Errorf("bootstrap: static assets: %w", err)
	}

	userDB := func(userID int64) (*sql.DB, error) {
		return mgr.User(ctx, userID)
	}

	// 行情数据源
	em := market.NewEastmoneySource()
	sina := market.NewSinaSource()
	cascade := market.NewCascade(em, sina, logger)

	// WebSocket hub
	wsHub := ws.NewHub(cascade, logger)
	wsHub.Start(ctx)

	// Job 调度器
	sched := job.NewScheduler(logger)
	sched.AddTradingDay("purge-expired-sessions", 6*time.Hour, func(ctx context.Context) {
		authSvc.PurgeExpiredSessions(ctx)
	})
	sched.Start(ctx)

	// LLM agent
	agentSvc := agent.NewService(agent.LoadConfig(), cascade, cascade, logger)

	engine := transport.New(transport.Deps{
		Logger: logger,
		Auth:   authSvc,
		UserDB: userDB,
		Market: transport.NewMarketHandler(cascade, cascade, em),
		Agent:  transport.NewAgentHandler(agentSvc),
		WSHub:  wsHub,
		Static: static,
	})

	return &App{Engine: engine, Store: mgr, Scheduler: sched, WSHub: wsHub}, nil
}

func buildStatic(logger *slog.Logger) (*transport.StaticHandler, error) {
	if dir := os.Getenv("ARGENT_STATIC_DIR"); dir != "" {
		logger.Info("serving frontend from disk", "dir", dir)
		return transport.NewStaticHandler(web.DirFS(dir), false, logger)
	}
	fsys, err := web.EmbeddedFS()
	if err != nil {
		return nil, err
	}
	return transport.NewStaticHandler(fsys, true, logger)
}

func buildLogger() *slog.Logger {
	cfg := log.Config{
		Level:  os.Getenv("ARGENT_LOG_LEVEL"),
		Format: os.Getenv("ARGENT_LOG_FORMAT"),
		Output: os.Getenv("ARGENT_LOG_OUTPUT"),
	}
	logger := log.New(cfg)
	slog.SetDefault(logger)
	if os.Getenv("ARGENT_ENV") == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	return logger
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
