package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/auth"
	"github.com/jwcen/argent-go/internal/infra/log"
	"github.com/jwcen/argent-go/internal/infra/sqlite"
	"github.com/jwcen/argent-go/internal/store"
	"github.com/jwcen/argent-go/internal/transport"
)

// App 是组装完成的应用，持有 HTTP 引擎与需要优雅关闭的资源。
type App struct {
	Engine *gin.Engine
	Store  *store.Manager
}

// Close 释放资源（数据库连接等）。
func (a *App) Close() error {
	if a.Store != nil {
		return a.Store.Close()
	}
	return nil
}

// Build 组装整个应用：config(日志) → store → repository → service → handler → router。
//
// 这是手写依赖注入（DI）的唯一入口：所有依赖在这里显式 new 出来、串起来。
// 好处是依赖关系一眼可见、无需反射魔法、编译期就能发现接线错误；
// 代价是随着模块增多这个函数会变长——届时按业务域拆成 buildXxx 小函数即可。
func Build(ctx context.Context) (*App, error) {
	logger := buildLogger()

	mgr, err := store.Open(ctx, store.Config{
		DataDir:  envOr("ARGENT_DATA_DIR", "./data"),
		LegacyDB: os.Getenv("ARGENT_LEGACY_DB"), // 可选：首个用户继承旧库
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: open store: %w", err)
	}

	// 依赖倒置的接线点：仓储实现（外层）被注入到业务服务（内层）声明的接口位上。
	authRepo := sqlite.NewAuthRepo(mgr.Global())
	authSvc := auth.NewService(
		authRepo,
		auth.NewLogMailer(logger), // TODO: 生产环境替换为 SMTP 实现
		mgr,                       // *store.Manager 实现了 auth.Provisioner
		auth.DefaultConfig(),
	)

	engine := transport.New(transport.Deps{
		Logger: logger,
		Auth:   authSvc,
	})

	return &App{Engine: engine, Store: mgr}, nil
}

// buildLogger 从环境变量构造结构化 logger，并设为 slog 默认，
// 使标准库与其它库（gin 内部、sqlite 驱动等）都能共用同一条日志。
func buildLogger() *slog.Logger {
	cfg := log.Config{
		Level:  os.Getenv("ARGENT_LOG_LEVEL"),
		Format: os.Getenv("ARGENT_LOG_FORMAT"),
		Output: os.Getenv("ARGENT_LOG_OUTPUT"),
	}
	logger := log.New(cfg)
	slog.SetDefault(logger)

	// 生产环境关闭 gin 的 debug 模式（不再打印路由表与 "Gin is in debug mode" 提示）
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
