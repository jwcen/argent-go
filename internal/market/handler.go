package market

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// MarketHandler 把行情数据源适配成 HTTP 接口。
type MarketHandler struct {
	quoter  Quoter
	kliner  KlineProvider
	indices IndexProvider
}

func NewMarketHandler(q Quoter, k KlineProvider, idx IndexProvider) *MarketHandler {
	return &MarketHandler{quoter: q, kliner: k, indices: idx}
}

func (h *MarketHandler) Register(r gin.IRouter) {
	g := r.Group("/market")
	g.GET("/quote/:code", h.Quote)
	g.GET("/quote", h.BatchQuote)
	g.GET("/history/:code", h.History)
	g.GET("/indices", h.Indices)
	g.GET("/trading-day", h.TradingDay)
	g.GET("/stock-search", h.StockSearch)
}

// GET /api/market/quote/:code — 单只报价
func (h *MarketHandler) Quote(c *gin.Context) {
	code := c.Param("code")
	quotes, err := h.quoter.Quote(c.Request.Context(), []string{code})
	if err != nil {
		c.JSON(500, gin.H{"detail": err.Error()})
		return
	}
	if q, ok := quotes[code]; ok {
		c.JSON(200, q)
		return
	}
	c.JSON(404, gin.H{"detail": "no data"})
}

// GET /api/market/quote?codes=600519,000001 — 批量报价
func (h *MarketHandler) BatchQuote(c *gin.Context) {
	codesStr := c.Query("codes")
	if codesStr == "" {
		c.JSON(400, gin.H{"detail": "codes param required"})
		return
	}
	codes := splitCodes(codesStr)
	quotes, err := h.quoter.Quote(c.Request.Context(), codes)
	if err != nil {
		c.JSON(500, gin.H{"detail": err.Error()})
		return
	}
	// 返回数组
	list := make([]*Quote, 0, len(quotes))
	for _, q := range quotes {
		list = append(list, q)
	}
	c.JSON(200, list)
}

// GET /api/market/history/:code?days=60 — 日K
func (h *MarketHandler) History(c *gin.Context) {
	code := c.Param("code")
	days := 60
	if d := c.Query("days"); d != "" {
		if v, err := strconv.Atoi(d); err == nil && v > 0 {
			days = v
		}
	}
	kl, err := h.kliner.Kline(c.Request.Context(), code, days)
	if err != nil {
		c.JSON(500, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(200, kl)
}

// GET /api/market/indices — 大盘指数
func (h *MarketHandler) Indices(c *gin.Context) {
	if h.indices == nil {
		c.JSON(200, []any{})
		return
	}
	idx, err := h.indices.Indices(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(200, idx)
}

// GET /api/market/trading-day — 今日是否交易日 + 下个交易日
func (h *MarketHandler) TradingDay(c *gin.Context) {
	now := time.Now()
	today := IsTradingDay(now)
	next := NextTradingDay(now)
	c.JSON(200, gin.H{
		"is_trading_day":   today,
		"today":            now.Format("2006-01-02"),
		"next_trading_day": next.Format("2006-01-02"),
	})
}

// GET /api/market/stock-search?keyword=茅台 — 简化搜索
// 完整搜索需要东财搜索 API，这里先返回空列表占位
func (h *MarketHandler) StockSearch(c *gin.Context) {
	c.JSON(200, []any{})
}

// splitCodes 拆分逗号分隔的股票码
func splitCodes(s string) []string {
	var codes []string
	for _, c := range splitAndTrim(s, ",") {
		if c != "" {
			codes = append(codes, c)
		}
	}
	return codes
}

func splitAndTrim(s, sep string) []string {
	var out []string
	for _, p := range split(s, sep) {
		p = trimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func split(s, sep string) []string {
	var out []string
	for i := 0; i < len(s); {
		j := indexByte(s[i:], sep[0])
		if j < 0 {
			out = append(out, s[i:])
			break
		}
		out = append(out, s[i:i+j])
		i += j + 1
	}
	if len(s) == 0 {
		return []string{""}
	}
	return out
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// 确保 http 包被引用（handler 里可能用到）
var _ = http.MethodGet
var _ = context.Background
