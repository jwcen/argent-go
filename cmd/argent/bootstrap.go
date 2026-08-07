package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/auth"
	"github.com/jwcen/argent-go/internal/infra/log"
	"github.com/jwcen/argent-go/internal/infra/sqlite"
	"github.com/jwcen/argent-go/internal/market"
	"github.com/jwcen/argent-go/internal/store"
	"github.com/jwcen/argent-go/internal/transport"
	"github.com/jwcen/argent-go/web"
)

type App struct {
	Engine *gin.Engine
	Store  *store.Manager
}

func (a *App) Close() error {
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

	// userDB 把 store.Manager.User 包成 transport 层需要的签名。
	// portfolio 等业务域的 handler 用它在请求时按 userID 取用户库句柄。
	userDB := func(userID int64) (*sql.DB, error) {
		return mgr.User(ctx, userID)
	}

	// 行情数据源：东财（主）→ 新浪（降级），cascade 装饰器自动切换。
	em := market.NewEastmoneySource()
	sina := market.NewSinaSource()
	cascade := market.NewCascade(em, sina, logger)

	engine := transport.New(transport.Deps{
		Logger: logger,
		Auth:   authSvc,
		UserDB: userDB,
		Market: market.NewMarketHandler(cascade, cascade, em),
		Static: static,
	})

	return &App{Engine: engine, Store: mgr}, nil
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
