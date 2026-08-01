package main

import (
	"log/slog"
	"os"
)

func main() {
	// 12-factor：端口可由环境变量覆盖（Python 原版用 8888，Go 新版用 8889 并行对拍）。
	port := os.Getenv("ARGENT_PORT")
	if port == "" {
		port = "8889"
	}

	r := Build()

	addr := ":" + port
	slog.Default().Info("argent-go starting", slog.String("addr", addr))
	if err := r.Run(addr); err != nil {
		slog.Default().Error("server exited", slog.Any("err", err))
		os.Exit(1)
	}
}
