package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

// testFS 用 fstest.MapFS 造一个内存文件系统。
//
// 这是 fs.FS 抽象最漂亮的回报：测试完全不碰磁盘、不碰 embed，
// 跑得飞快且没有任何环境依赖。跟 Stage 2 用 fakeRepo 测 auth.Service 是同一个套路。
func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":         {Data: []byte("<!doctype html><title>Argent</title>")},
		"sw.js":              {Data: []byte("// service worker")},
		"manifest.json":      {Data: []byte(`{"name":"Argent"}`)},
		"icon-192.svg":       {Data: []byte("<svg/>")},
		"assets/app-abc.js":  {Data: []byte("console.log(1)")},
		"assets/app-abc.css": {Data: []byte("body{}")},
	}
}

func newTestEngine(t *testing.T, precompute bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	h, err := NewStaticHandler(testFS(), precompute, nil)
	if err != nil {
		t.Fatalf("NewStaticHandler: %v", err)
	}

	r := gin.New()
	r.HandleMethodNotAllowed = true
	r.NoMethod(func(c *gin.Context) {
		WriteError(c, http.StatusMethodNotAllowed, "method not allowed")
	})
	// 模拟一个已存在的 API 路由，用来验证静态服务不会抢走 /api 的流量。
	r.GET("/api/health", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	h.Register(r)
	return r
}

func do(t *testing.T, r *gin.Engine, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestStatic_ServesIndexAtRoot(t *testing.T) {
	r := newTestEngine(t, true)

	w := do(t, r, http.MethodGet, "/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != cacheRevalidate {
		t.Errorf("Cache-Control = %q, want %q", got, cacheRevalidate)
	}
	if w.Header().Get("ETag") == "" {
		t.Error("ETag missing")
	}
}

// 缓存分档是本阶段的核心策略，用表驱动一次覆盖。
func TestStatic_CacheControlTiers(t *testing.T) {
	r := newTestEngine(t, true)

	tests := []struct {
		path string
		want string
	}{
		{"/", cacheRevalidate},                  // index.html 是指向 assets 的指针
		{"/index.html", cacheRevalidate},        // 同上，显式路径
		{"/sw.js", cacheRevalidate},             // SW 卡住比页面卡住更难修
		{"/assets/app-abc.js", cacheImmutable},  // 文件名带 hash
		{"/assets/app-abc.css", cacheImmutable}, // 同上
		{"/manifest.json", cacheShort},
		{"/icon-192.svg", cacheShort},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			w := do(t, r, http.MethodGet, tt.path, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", w.Code)
			}
			if got := w.Header().Get("Cache-Control"); got != tt.want {
				t.Errorf("Cache-Control = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatic_ContentTypes(t *testing.T) {
	r := newTestEngine(t, true)

	tests := []struct{ path, want string }{
		{"/assets/app-abc.js", "text/javascript; charset=utf-8"},
		{"/assets/app-abc.css", "text/css; charset=utf-8"},
		{"/manifest.json", "application/json; charset=utf-8"},
		{"/icon-192.svg", "image/svg+xml"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			w := do(t, r, http.MethodGet, tt.path, nil)
			if got := w.Header().Get("Content-Type"); got != tt.want {
				t.Errorf("Content-Type = %q, want %q", got, tt.want)
			}
		})
	}
}

// 条件请求：这正是 embed.FS 的 ModTime 为零值时会失效、
// 必须靠自算 ETag 补回来的能力。
func TestStatic_ConditionalRequestReturns304(t *testing.T) {
	for _, precompute := range []bool{true, false} {
		name := "precomputed"
		if !precompute {
			name = "realtime"
		}
		t.Run(name, func(t *testing.T) {
			r := newTestEngine(t, precompute)

			first := do(t, r, http.MethodGet, "/assets/app-abc.js", nil)
			etag := first.Header().Get("ETag")
			if etag == "" {
				t.Fatal("first response has no ETag")
			}

			second := do(t, r, http.MethodGet, "/assets/app-abc.js",
				map[string]string{"If-None-Match": etag})
			if second.Code != http.StatusNotModified {
				t.Fatalf("status = %d, want 304", second.Code)
			}
			if second.Body.Len() != 0 {
				t.Errorf("304 must have empty body, got %d bytes", second.Body.Len())
			}
			// 304 仍需带 Cache-Control，客户端要靠它刷新缓存有效期。
			if second.Header().Get("Cache-Control") == "" {
				t.Error("304 missing Cache-Control")
			}
		})
	}
}

func TestStatic_ETagMismatchReturnsFullBody(t *testing.T) {
	r := newTestEngine(t, true)
	w := do(t, r, http.MethodGet, "/assets/app-abc.js",
		map[string]string{"If-None-Match": `"stale00000000000"`})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "console.log(1)" {
		t.Errorf("body = %q", w.Body.String())
	}
}

// 最重要的一条：/api 下不存在的路径必须是 JSON 404，
// 绝不能回落 index.html —— 否则前端 res.json() 会报
// "Unexpected token '<'"，错误信息与真实原因完全无关。
func TestStatic_UnknownAPIPathReturnsJSON404(t *testing.T) {
	r := newTestEngine(t, true)

	for _, p := range []string{"/api/nope", "/api/portfolio/holdings", "/api"} {
		t.Run(p, func(t *testing.T) {
			w := do(t, r, http.MethodGet, p, nil)
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", w.Code)
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
				t.Errorf("Content-Type = %q, want JSON", ct)
			}
			if body := w.Body.String(); body != `{"detail":"not found"}` {
				t.Errorf("body = %q", body)
			}
		})
	}
}

func TestStatic_ExistingAPIRouteStillWorks(t *testing.T) {
	r := newTestEngine(t, true)
	w := do(t, r, http.MethodGet, "/api/health", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// 已注册路由用错方法 → 405 且错误体仍是 {"detail": ...}
func TestStatic_MethodNotAllowedIsJSON(t *testing.T) {
	r := newTestEngine(t, true)
	w := do(t, r, http.MethodPost, "/api/health", nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
	if body := w.Body.String(); body != `{"detail":"method not allowed"}` {
		t.Errorf("body = %q", body)
	}
}

func TestStatic_MissingAssetReturns404NotIndex(t *testing.T) {
	r := newTestEngine(t, true)

	// 带扩展名 = 实打实的资源缺失，必须 404，不能悄悄回落 HTML。
	w := do(t, r, http.MethodGet, "/assets/gone-xyz.js", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if body := w.Body.String(); body != `{"detail":"not found"}` {
		t.Errorf("body = %q", body)
	}
}

func TestStatic_UnknownPathWithoutExtFallsBackToIndex(t *testing.T) {
	r := newTestEngine(t, true)

	w := do(t, r, http.MethodGet, "/portfolio", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want HTML", got)
	}
}

// 目录穿越：path.Clean + fs.ValidPath 双保险
func TestStatic_RejectsTraversal(t *testing.T) {
	r := newTestEngine(t, true)

	// httptest.NewRequest 会保留原始路径，gin 交给我们时仍含 ../
	w := do(t, r, http.MethodGet, "/../go.mod", nil)
	if w.Code == http.StatusOK && w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("traversal leaked a real file: status=%d ct=%q",
			w.Code, w.Header().Get("Content-Type"))
	}
}

func TestStatic_PostToStaticPathIs404(t *testing.T) {
	r := newTestEngine(t, true)
	w := do(t, r, http.MethodPost, "/index.html", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestStatic_HeadRequestHasNoBody(t *testing.T) {
	r := newTestEngine(t, true)
	w := do(t, r, http.MethodHead, "/index.html", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Header().Get("ETag") == "" {
		t.Error("HEAD should still carry ETag")
	}
}

func TestETagMatches(t *testing.T) {
	tests := []struct {
		header, tag string
		want        bool
	}{
		{"", `"abc"`, false},
		{"*", `"abc"`, true},
		{`"abc"`, `"abc"`, true},
		{`"xyz"`, `"abc"`, false},
		{`"xyz", "abc"`, `"abc"`, true},
		{`W/"abc"`, `"abc"`, true},
		{`  "abc"  `, `"abc"`, true},
	}
	for _, tt := range tests {
		if got := etagMatches(tt.header, tt.tag); got != tt.want {
			t.Errorf("etagMatches(%q, %q) = %v, want %v", tt.header, tt.tag, got, tt.want)
		}
	}
}
