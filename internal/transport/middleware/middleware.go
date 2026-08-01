// Package middleware 提供 gin 的跨请求中间件（请求 ID、结构化访问日志、panic 恢复）。
//
// 这些中间件把日志能力“注入”到 gin，但日志本身由 internal/infra/log 提供，
// 因此与具体 Web 框架解耦——这符合简洁架构：transport 只是框架适配器层，
// 真正的日志内核不依赖 gin。
package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/infra/log"
)

// RequestID 生成或透传 X-Request-ID，并注入 context，
// 使后续所有通过 context 写入的日志都自动带上它（见 log.ctxHandler）。
// 透传头部可让网关 / 前端把一次调用链串起来做分布式追踪。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-ID")
		if rid == "" {
			rid = newRequestID()
		}
		c.Set("request_id", rid)
		c.Header("X-Request-ID", rid)

		ctx := log.ContextWithRequestID(c.Request.Context(), rid)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// RequestLogger 记录每条 HTTP 请求的结构化访问日志。
// 通过 c.Request.Context() 写入，request_id 由 ctxHandler 自动附加，无需手动 With。
func RequestLogger(logger *slog.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		// 把 logger 也放进 context，handler 可用 log.FromContext 取用
		ctx := log.WithLogger(c.Request.Context(), logger)
		// 注意：下面再用 c.Request.Context() 时拿到的已是带 logger 的版本
		c.Request = c.Request.WithContext(ctx)

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		logger.InfoContext(c.Request.Context(), "http request",
			slog.String("method", c.Request.Method),
			slog.String("path", path),
			slog.String("query", query),
			slog.Int("status", status),
			slog.Duration("latency", latency),
			slog.String("ip", c.ClientIP()),
			slog.String("user_agent", c.Request.UserAgent()),
		)
	}
}

// Recovery 捕获 panic 并以 error 级结构化日志记录完整堆栈，避免进程崩溃。
// 同时返回 500 给客户端，错误体保持 {"detail": ...} 与 WriteError 一致。
func Recovery(logger *slog.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				buf := make([]byte, 8192)
				n := runtime.Stack(buf, false)
				logger.ErrorContext(c.Request.Context(), "panic recovered",
					slog.Any("error", r),
					slog.String("stack", string(buf[:n])),
					slog.String("path", c.Request.URL.Path),
				)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"detail": "internal server error"})
			}
		}()
		c.Next()
	}
}

// newRequestID 用 crypto/rand 生成 16 字节十六进制 ID（无三方依赖）。
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read 在常规环境下不会失败；失败时退化为纳秒时间戳
		return time.Now().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b[:])
}
