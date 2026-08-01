package main

import (
	"log/slog"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/infra/log"
	"github.com/jwcen/argent-go/internal/transport"
)

// Build 组装整个应用：config(日志) → store(后续) → services(后续) → handlers(后续) → router → server。
// 现在是手写依赖注入（DI）的唯一入口：所有依赖在这里显式 new 出来、串起来，
// 避免包级全局变量满天飞。后续 Stage 往里逐步注入 store / service / handler。
func Build() *gin.Engine {
	return transport.New(buildLogger())
}

// buildLogger 从环境变量构造结构化 logger，并设为 slog 默认，
// 使标准库与其它库（gin 内部、future sqlite 等）都能共用同一条日志。
func buildLogger() *slog.Logger {
	cfg := log.Config{
		Level:  os.Getenv("ARGENT_LOG_LEVEL"),
		Format: os.Getenv("ARGENT_LOG_FORMAT"),
		Output: os.Getenv("ARGENT_LOG_OUTPUT"),
	}
	logger := log.New(cfg)
	slog.SetDefault(logger)

	// 生产环境关闭 gin 的 debug 模式（不再打印路由表与 “Gin is in debug mode” 提示）
	if os.Getenv("ARGENT_ENV") == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	return logger
}
