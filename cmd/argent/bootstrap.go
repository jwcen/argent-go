package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
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
	loadDotEnv(".env")

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

	// 基金净值查询走新浪 f_ 接口（A 股报价源无法复用）
	mh := transport.NewMarketHandler(cascade, cascade, em, logger)
	mh.SetFundQuoter(sina)
	mh.SetSearcher(em) // 股票搜索（东财 suggest API）

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
	agentSvc.SetFundQuoter(sina)           // 基金净值走新浪 f_ 接口
	agentSvc.SetIndexProvider(cascade)     // 大盘指数（cascade 已实现 IndexProvider）
	agentSvc.SetSectorProvider(cascade)    // 板块/市场宽度/海外指数（cascade 已实现 SectorProvider）

	engine := transport.New(transport.Deps{
		Logger: logger,
		Auth:   authSvc,
		UserDB: userDB,
		Market: mh,
		Quoter: cascade,
		Kline:  cascade,
		Agent:  transport.NewAgentHandler(agentSvc, userDB),
		Import: transport.NewImportHandler(agentSvc, logger),
		Strategy: transport.NewStrategyHandler(userDB, cascade, cascade),
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

// loadDotEnv 读取当前目录的 .env 文件，把 KEY=VALUE 注入环境变量。
// 仅注入尚未设置的键（不覆盖已存在的环境/Shell 变量）。
// 文件不存在或某行无法解析时静默跳过——没有 .env 也能正常运行（走默认值）。
// .env 含密钥，已在 .gitignore 中排除，不进版本库。
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		v = strings.Trim(v, `"'`)
		if k != "" && os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
}
