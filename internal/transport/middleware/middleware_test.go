package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/infra/log"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestEngine(logger *slog.Logger) *gin.Engine {
	r := gin.New()
	r.Use(RequestID())
	r.Use(Recovery(logger))
	r.Use(RequestLogger(logger))
	return r
}

func captureLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	// 用 log.NewToWriter 走与线上一致的 ctxHandler 包装，确保 request_id 自动注入
	l := log.NewToWriter(&buf, "debug", "json")
	return l, &buf
}

func TestRequestIDGeneratedAndPropagated(t *testing.T) {
	logger, buf := captureLogger()
	r := newTestEngine(logger)
	r.GET("/ping", func(c *gin.Context) {
		c.String(200, log.RequestIDFromContext(c.Request.Context()))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	r.ServeHTTP(w, req)

	rid := w.Body.String()
	if rid == "" {
		t.Fatal("expected a generated request id")
	}
	if got := w.Header().Get("X-Request-ID"); got != rid {
		t.Fatalf("X-Request-ID header = %q, want %q", got, rid)
	}
	// 访问日志应自动带上同一个 request_id
	if !bytes.Contains(buf.Bytes(), []byte(rid)) {
		t.Fatalf("log output missing request_id %q: %s", rid, buf.String())
	}
}

func TestRequestIDTransparent(t *testing.T) {
	logger, _ := captureLogger()
	r := newTestEngine(logger)
	r.GET("/ping", func(c *gin.Context) {
		c.String(200, log.RequestIDFromContext(c.Request.Context()))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("X-Request-ID", "trace-xyz")
	r.ServeHTTP(w, req)

	if w.Body.String() != "trace-xyz" {
		t.Fatalf("expected transparent request id trace-xyz, got %q", w.Body.String())
	}
}

func TestRecoveryCatchesPanic(t *testing.T) {
	logger, buf := captureLogger()
	r := newTestEngine(logger)
	r.GET("/boom", func(c *gin.Context) { panic("kaboom") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("internal server error")) {
		t.Fatalf("body missing detail: %s", w.Body.String())
	}

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("panic log not JSON: %v", err)
	}
	if entry["msg"] != "panic recovered" {
		t.Fatalf("expected 'panic recovered' log, got %v", entry["msg"])
	}
	if _, ok := entry["stack"]; !ok {
		t.Fatalf("expected stack in panic log, got %s", buf.String())
	}
}
