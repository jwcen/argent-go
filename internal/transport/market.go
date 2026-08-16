package transport

import (
	"log/slog"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/market"
)

// MarketHandler 把行情数据源适配成 HTTP 接口。
//
// 行情是 best-effort 外部依赖：沙箱无外网或数据源抖动时，所有读接口都
// 优雅降级为「空数据 + 200」，而不是 500（原则：sandbox 无源优雅降级）。
type MarketHandler struct {
	quoter   market.Quoter
	kliner   market.KlineProvider
	indices  market.IndexProvider
	funds    market.FundQuoter      // 场外基金官方净值；nil 时 /funds 返回空
	fundEst  market.FundEstimator   // 场外基金盘中估值；nil 时 /funds-estimate 返回空
	searcher market.Searcher        // 股票搜索；nil 时 /stock-search 返回空
	logger   *slog.Logger
}

func NewMarketHandler(q market.Quoter, k market.KlineProvider, idx market.IndexProvider, logger *slog.Logger) *MarketHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &MarketHandler{quoter: q, kliner: k, indices: idx, logger: logger}
}

// SetFundQuoter 注入场外基金净值查询源（可选；不注入则 /api/market/funds 返回空）。
func (h *MarketHandler) SetFundQuoter(f market.FundQuoter) {
	h.funds = f
}

// SetFundEstimator 注入场外基金盘中估值查询源（可选；不注入则 /api/market/funds-estimate 返回空）。
func (h *MarketHandler) SetFundEstimator(e market.FundEstimator) {
	h.fundEst = e
}

// SetSearcher 注入股票搜索源（可选；不注入则 /api/market/stock-search 返回空）。
func (h *MarketHandler) SetSearcher(s market.Searcher) {
	h.searcher = s
}

func (h *MarketHandler) Register(r gin.IRouter) {
	g := r.Group("/market")
	g.GET("/quote/:code", h.Quote)
	g.GET("/quote", h.BatchQuote)
	g.GET("/funds", h.Funds)
	g.GET("/funds-estimate", h.FundsEstimate)
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
		// 数据源不可达（沙箱无外网等）：降级为空，不 500
		h.logger.Warn("market quote failed, degrade to empty", "code", code, "err", err)
		c.JSON(200, nil)
		return
	}
	if q, ok := quotes[code]; ok {
		c.JSON(200, q)
		return
	}
	// 有数据源但查不到该 code：同样返回空（非错误）
	c.JSON(200, nil)
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
		h.logger.Warn("market batch quote failed, degrade to empty", "err", err)
		c.JSON(200, []any{})
		return
	}
	// 返回数组
	list := make([]*market.Quote, 0, len(quotes))
	for _, q := range quotes {
		list = append(list, q)
	}
	c.JSON(200, list)
}

// GET /api/market/funds?codes=110011,161725 — 场外基金净值批量查询
func (h *MarketHandler) Funds(c *gin.Context) {
	if h.funds == nil {
		c.JSON(200, []any{})
		return
	}
	codesStr := c.Query("codes")
	if codesStr == "" {
		c.JSON(400, gin.H{"detail": "codes param required"})
		return
	}
	fqs, err := h.funds.QuoteFunds(c.Request.Context(), splitCodes(codesStr))
	if err != nil {
		h.logger.Warn("market funds failed, degrade to empty", "err", err)
		c.JSON(200, []any{})
		return
	}
	list := make([]*market.FundQuote, 0, len(fqs))
	for _, f := range fqs {
		list = append(list, f)
	}
	c.JSON(200, list)
}

// GET /api/market/funds-estimate?codes=... — 场外基金盘中估值（盘内 gsz>0，盘外 gsz=0）
func (h *MarketHandler) FundsEstimate(c *gin.Context) {
	if h.fundEst == nil {
		c.JSON(200, []any{})
		return
	}
	codesStr := c.Query("codes")
	if codesStr == "" {
		c.JSON(400, gin.H{"detail": "codes param required"})
		return
	}
	ests, err := h.fundEst.EstimateFunds(c.Request.Context(), splitCodes(codesStr))
	if err != nil {
		h.logger.Warn("market funds-estimate failed, degrade to empty", "err", err)
		c.JSON(200, []any{})
		return
	}
	list := make([]*market.FundEstimate, 0, len(ests))
	for _, e := range ests {
		list = append(list, e)
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
	kl, err := h.kliner.Kline(c.Request.Context(), code, market.PeriodDaily, days)
	if err != nil {
		h.logger.Warn("market history failed, degrade to empty", "err", err)
		c.JSON(200, []any{})
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
		h.logger.Warn("market indices failed, degrade to empty", "err", err)
		c.JSON(200, []any{})
		return
	}
	c.JSON(200, idx)
}

// GET /api/market/trading-day — 今日是否交易日 + 下个交易日
func (h *MarketHandler) TradingDay(c *gin.Context) {
	now := time.Now()
	today := market.IsTradingDay(now)
	next := market.NextTradingDay(now)
	c.JSON(200, gin.H{
		"is_trading_day":   today,
		"today":            now.Format("2006-01-02"),
		"next_trading_day": next.Format("2006-01-02"),
	})
}

// GET /api/market/stock-search?keyword=茅台&limit=10 — 股票代码/名称模糊搜索
func (h *MarketHandler) StockSearch(c *gin.Context) {
	if h.searcher == nil {
		c.JSON(200, []any{})
		return
	}
	keyword := c.Query("keyword")
	if keyword == "" {
		c.JSON(200, []any{})
		return
	}
	limit := 10
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 20 {
			limit = v
		}
	}
	results, err := h.searcher.Search(c.Request.Context(), keyword, limit)
	if err != nil {
		h.logger.Warn("market stock-search failed, degrade to empty", "keyword", keyword, "err", err)
		c.JSON(200, []any{})
		return
	}
	c.JSON(200, results)
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
