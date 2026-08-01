package log

import (
	"context"
	"log/slog"
)

// ContextWithRequestID 把请求 ID 注入 context，供 ctxHandler 自动写入每条日志。
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromContext 从 context 取出请求 ID；未设置时返回空串。
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// WithLogger 把 logger 存入 context，便于 handler 通过 FromContext 取用，
// 而不必依赖包级全局变量（符合架构“不要全局可变状态”的约束）。
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

// FromContext 从 context 取出 logger；未设置时返回 slog.Default()，保证永不 nil。
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
