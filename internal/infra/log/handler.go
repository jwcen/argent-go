package log

import (
	"context"
	"log/slog"
)

// ctxKey 是存放在 context 中的内部键类型，用未导出类型避免与其它包冲突。
type ctxKey string

const (
	requestIDKey ctxKey = "request_id"
	loggerKey    ctxKey = "logger"
)

// ctxHandler 包装任意 slog.Handler，在 Handle 阶段把 context 里携带的
// 请求级字段（request_id 等）合并进每条日志记录。
//
// 为什么这样做？生产项目里，handler / service 调用 slog 时如果每次都手写
// .With("request_id", ...)，既啰嗦又容易漏。把 request_id 放在 context 里，
// 由 ctxHandler 在落盘前自动附加，调用点只需保证传入正确的 context 即可。
type ctxHandler struct {
	handler slog.Handler
}

func (h *ctxHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *ctxHandler) Handle(ctx context.Context, rec slog.Record) error {
	if v, ok := ctx.Value(requestIDKey).(string); ok && v != "" {
		rec.AddAttrs(slog.String("request_id", v))
	}
	return h.handler.Handle(ctx, rec)
}

func (h *ctxHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ctxHandler{handler: h.handler.WithAttrs(attrs)}
}

func (h *ctxHandler) WithGroup(name string) slog.Handler {
	return &ctxHandler{handler: h.handler.WithGroup(name)}
}
