package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// 12-factor：端口可由环境变量覆盖（Python 原版用 8888，Go 新版用 8889 并行对拍）。
	port := os.Getenv("ARGENT_PORT")
	if port == "" {
		port = "8889"
	}

	ctx := context.Background()

	app, err := Build(ctx)
	if err != nil {
		slog.Default().Error("bootstrap failed", slog.Any("err", err))
		os.Exit(1)
	}
	defer func() { _ = app.Close() }()

	addr := ":" + port
	srv := &http.Server{
		Addr:              addr,
		Handler:           app.Engine,
		ReadHeaderTimeout: 10 * time.Second, // 抵御 Slowloris 慢速攻击
	}

	// 在独立 goroutine 里启动服务，主 goroutine 留给信号处理。
	go func() {
		slog.Default().Info("argent-go starting", slog.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Default().Error("server exited", slog.Any("err", err))
			os.Exit(1)
		}
	}()

	// 优雅关闭：收到 SIGINT/SIGTERM 后停止接受新连接，
	// 给正在处理的请求 10 秒收尾时间，再关闭数据库连接。
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Default().Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Default().Error("graceful shutdown failed", slog.Any("err", err))
	}
	if err := app.Close(); err != nil {
		slog.Default().Error("closing resources failed", slog.Any("err", err))
	}
	slog.Default().Info("argent-go stopped")
}
