package log

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"INFO":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"unknown": slog.LevelInfo, // 未知值回落 info
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNewNotNil(t *testing.T) {
	if l := New(Config{Level: "debug", Format: "text"}); l == nil {
		t.Fatal("New returned nil")
	}
}

// TestCtxHandlerInjectsRequestID 验证 ctxHandler 会把 context 中的 request_id 自动附加到日志。
func TestCtxHandlerInjectsRequestID(t *testing.T) {
	var sb strings.Builder
	base := slog.NewJSONHandler(&sb, &slog.HandlerOptions{Level: slog.LevelDebug})
	cl := slog.New(&ctxHandler{handler: base})

	ctx := ContextWithRequestID(context.Background(), "req-123")
	cl.InfoContext(ctx, "hello")

	out := sb.String()
	if !strings.Contains(out, "req-123") {
		t.Fatalf("expected request_id in log output, got: %s", out)
	}
	if !strings.Contains(out, `"msg":"hello"`) {
		t.Fatalf("expected message in log output, got: %s", out)
	}
}

func TestRequestIDRoundTrip(t *testing.T) {
	ctx := ContextWithRequestID(context.Background(), "abc")
	if got := RequestIDFromContext(ctx); got != "abc" {
		t.Errorf("RequestIDFromContext = %q, want abc", got)
	}
	if got := RequestIDFromContext(context.Background()); got != "" {
		t.Errorf("RequestIDFromContext empty ctx = %q, want empty", got)
	}
}

func TestWithAndFromContext(t *testing.T) {
	custom := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	ctx := WithLogger(context.Background(), custom)
	if FromContext(ctx) != custom {
		t.Error("FromContext did not return the injected logger")
	}
	if FromContext(context.Background()) != slog.Default() {
		t.Error("FromContext should fall back to slog.Default()")
	}
}
