package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// 缓存三档策略。
//
// 关键认知：no-cache 不是「不要缓存」，而是「可以存，但每次用之前必须回源校验」，
// 配合 ETag 就能拿 304（省流量但保正确）。真正的「别存」是 no-store。
// 这两个名字在 HTTP 规范里起反了，是经典误解来源。
const (
	// 文件名自带内容 hash（index-B5DGIFpn.js），内容变则文件名变，可以永久缓存。
	cacheImmutable = "public, max-age=31536000, immutable"

	// index.html 是指向 assets 的唯一「指针」，一旦被强缓存，
	// 用户就永远拿着旧指针，发多少版都看不到新代码。
	// sw.js 同理，而且 Service Worker 卡住比页面卡住更难修。
	cacheRevalidate = "no-cache"

	// 图标 / manifest：变得少，但不是永不变。
	cacheShort = "public, max-age=3600"
)

// contentTypes 自己维护一张小表，而不是完全依赖 mime.TypeByExtension。
// 原因：mime 包会读取系统的 /etc/mime.types，不同机器结果可能不同
// （比如 .js 在 macOS 上会变成 application/javascript）。
// 静态资源类型就这么几种，显式列出来换取跨平台可预测性。
var contentTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".ico":   "image/x-icon",
	".webp":  "image/webp",
	".woff2": "font/woff2",
	".txt":   "text/plain; charset=utf-8",
}

// StaticHandler 把一个 fs.FS 暴露成 HTTP 静态文件服务。
//
// 它不关心背后是 embed.FS 还是磁盘目录——这正是 fs.FS 这个接口的价值：
// 依赖倒置的又一次落地，跟 Stage 2 里 auth.Repository 是同一个思路。
type StaticHandler struct {
	fsys   fs.FS
	logger *slog.Logger

	// etags 是启动时预算好的「文件路径 → ETag」。
	//
	// 为什么必须自己算？因为 embed.FS 的 ModTime() 返回零值，
	// http.ServeContent 看到零时间就不会发 Last-Modified，
	// 而标准库从不自动生成 ETag —— 结果是条件请求全部失效，
	// no-cache 的资源每次都要全量重传。
	//
	// 磁盘模式下这张表是空的，改成每次请求实时计算，
	// 这样本地改文件刷新就能看到效果，不需要重启服务。
	etags map[string]string
}

// NewStaticHandler 构造静态服务。
//
// precompute=true 时遍历整个 FS 预算 ETag（用于 embed：内容编译期固定，算一次即可）；
// false 时留空表走实时计算（用于磁盘开发模式：文件随时会变）。
func NewStaticHandler(fsys fs.FS, precompute bool, logger *slog.Logger) (*StaticHandler, error) {
	if logger == nil {
		logger = slog.Default()
	}
	h := &StaticHandler{fsys: fsys, logger: logger, etags: map[string]string{}}
	if !precompute {
		return h, nil
	}

	// fs.WalkDir 是标准库提供的通用遍历，对任何 fs.FS 都能用。
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		h.etags[p] = computeETag(data)
		return nil
	})
	if err != nil {
		return nil, err
	}
	h.logger.Debug("static assets indexed", "files", len(h.etags))
	return h, nil
}

// Register 把静态服务挂到引擎上。
//
// 注意这里没有用 r.Static("/", ...)。gin 的路由是 radix tree，
// 在根注册 /*filepath 会和已存在的 /api 前缀冲突，服务启动瞬间 panic：
//
//	catch-all wildcard '*filepath' in new path '/*filepath'
//	conflicts with existing path segment 'api'
//
// NoRoute 是路由树之外的兜底钩子，天然不参与冲突检测，
// 所有没匹配上任何路由的请求都落到它手里，正好适合「其余都当静态文件找」。
func (h *StaticHandler) Register(r *gin.Engine) {
	r.NoRoute(h.handle)
}

func (h *StaticHandler) handle(c *gin.Context) {
	p := c.Request.URL.Path

	// 最高优先级：/api 下的未匹配路径必须返回 JSON 404。
	//
	// 如果把 index.html 喂给前端的 fetch，res.json() 会在解析 HTML 时
	// 抛出 "Unexpected token '<'"，错误信息和真实原因（路由不存在）
	// 毫无关系，排查起来极其痛苦。
	if p == "/api" || strings.HasPrefix(p, "/api/") {
		WriteError(c, http.StatusNotFound, "not found")
		return
	}

	// 静态资源只接受读方法。
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		WriteError(c, http.StatusNotFound, "not found")
		return
	}

	// path.Clean 会消解 ../ 等花样；fs.ReadFile 还会再用 fs.ValidPath 校验一次。
	// 双保险防目录穿越。
	name := strings.TrimPrefix(path.Clean(p), "/")
	if name == "" || name == "." {
		name = "index.html"
	}

	if h.exists(name) {
		h.serve(c, name)
		return
	}

	// 走到这里说明文件不存在。
	// 带扩展名的（.js/.css/.png…）就是实打实的资源缺失，老实返回 404；
	// 不带扩展名的当作前端路由，回落 index.html —— 前端用的是 hash 路由，
	// 正常不会走到这，但用户手动敲个 /portfolio 时不至于白屏。
	if path.Ext(name) != "" {
		WriteError(c, http.StatusNotFound, "not found")
		return
	}
	h.serve(c, "index.html")
}

func (h *StaticHandler) exists(name string) bool {
	f, err := h.fsys.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func (h *StaticHandler) serve(c *gin.Context, name string) {
	// 快路径：预算过 ETag 且客户端手里的版本一致，直接 304，连文件都不用读。
	if tag, ok := h.etags[name]; ok && etagMatches(c.GetHeader("If-None-Match"), tag) {
		h.setCacheHeaders(c, name, tag)
		c.Status(http.StatusNotModified)
		return
	}

	data, err := fs.ReadFile(h.fsys, name)
	if err != nil {
		h.logger.Warn("static read failed", "path", name, "err", err)
		WriteError(c, http.StatusNotFound, "not found")
		return
	}

	tag, ok := h.etags[name]
	if !ok {
		tag = computeETag(data) // 磁盘模式：实时算
	}
	if etagMatches(c.GetHeader("If-None-Match"), tag) {
		h.setCacheHeaders(c, name, tag)
		c.Status(http.StatusNotModified)
		return
	}

	h.setCacheHeaders(c, name, tag)
	c.Data(http.StatusOK, contentTypeFor(name), data)
}

func (h *StaticHandler) setCacheHeaders(c *gin.Context, name, tag string) {
	c.Header("ETag", tag)
	c.Header("Cache-Control", cacheControlFor(name))
}

func cacheControlFor(name string) string {
	switch {
	case strings.HasPrefix(name, "assets/"):
		return cacheImmutable
	case name == "index.html", name == "sw.js":
		return cacheRevalidate
	default:
		return cacheShort
	}
}

func contentTypeFor(name string) string {
	if ct, ok := contentTypes[strings.ToLower(path.Ext(name))]; ok {
		return ct
	}
	return "application/octet-stream"
}

// computeETag 取内容 sha256 的前 8 字节。
// 16 个 hex 字符足够避免碰撞，又比完整 64 字符省头部空间。
// 按 HTTP 规范 ETag 必须是 quoted-string，所以带上引号。
func computeETag(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

// etagMatches 解析 If-None-Match 头。
// 它可以是 * 、单个 tag、或逗号分隔的多个 tag，还可能带弱校验前缀 W/。
func etagMatches(header, tag string) bool {
	if header == "" {
		return false
	}
	if strings.TrimSpace(header) == "*" {
		return true
	}
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == tag {
			return true
		}
	}
	return false
}
