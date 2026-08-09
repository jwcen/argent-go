package transport

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/auth"
	"github.com/jwcen/argent-go/internal/infra/sqlite"
	"github.com/jwcen/argent-go/internal/market"
	"github.com/jwcen/argent-go/internal/portfolio"
)

// PortfolioHandler 把 portfolio.Service 适配成 HTTP 接口。
//
// 注意：portfolio 数据存在每用户独立库（users/u{id}.db）里，
// 所以不能在 bootstrap 时固定一个 repo——需要按请求获取当前用户的 DB。
// dbFn 就是这个「按 userID 取 *sql.DB」的函数，由 bootstrap 注入 store.Manager.User。
// kline 用于净值曲线叠加沪深300基准；nil 时不叠加（沙箱无外网时即如此）。
// quoter 用于首次创建持仓时自动查询股票名称；nil 则跳过（保持空）。
type PortfolioHandler struct {
	dbFn   func(userID int64) (*sql.DB, error)
	kline  market.KlineProvider
	quoter market.Quoter
}

func NewPortfolioHandler(dbFn func(userID int64) (*sql.DB, error), kline market.KlineProvider, quoter market.Quoter) *PortfolioHandler {
	return &PortfolioHandler{dbFn: dbFn, kline: kline, quoter: quoter}
}

// Register 挂载 /api/portfolio 和 /api/brokers 下的路由。
// 必须挂在受 RequireAuth 保护的分组上。
func (h *PortfolioHandler) Register(r gin.IRouter) {
	g := r.Group("/portfolio")
	g.GET("", h.ListHoldings)
	g.GET("/realized", h.Realized)
	g.GET("/thesis", h.ListThesis)
	g.GET("/thesis/:code", h.GetThesis)
	g.PUT("/thesis/:code", h.UpsertThesis)
	g.DELETE("/thesis/:code", h.DeleteThesis)
	g.GET("/:code/actions", h.ListActions)
	g.POST("/:code/actions", h.CreateAction)
	g.PUT("/actions/:id", h.UpdateAction)
	g.DELETE("/actions/:id", h.DeleteAction)
	g.GET("/:code/dividends", h.ListDividendEvents)
	g.POST("/:code/dividends", h.UpsertDividendEvent)
	g.DELETE("/dividends/:id", h.DeleteDividendEvent)
	g.GET("/curve", h.Curve)

	wl := r.Group("/watchlist")
	wl.GET("", h.ListWatchlist)
	wl.POST("", h.AddWatchlist)
	wl.DELETE("/:code", h.RemoveWatchlist)

	b := r.Group("/brokers")
	b.GET("", h.ListBrokers)
	b.POST("", h.CreateBroker)
	b.PUT("/:id", h.UpdateBroker)
	b.DELETE("/:id", h.DeleteBroker)

	a := r.Group("/accounts")
	a.GET("", h.ListAccounts)
	a.POST("", h.CreateAccount)
	a.PUT("/:id", h.UpdateAccount)
	a.DELETE("/:id", h.DeleteAccount)
	a.GET("/summaries", h.AccountSummaries)
}

// svc 从 gin context 取出当前用户，获取其用户库，构造一次性的 service。
// 每次 request 都走这条路径——sql.DB 本身被 store.Manager 缓存，开销极低。
func (h *PortfolioHandler) svc(c *gin.Context) (*portfolio.Service, error) {
	uid := auth.MustUserID(c.Request.Context())
	if uid == 0 {
		return nil, errors.New("no user in context")
	}
	db, err := h.dbFn(uid)
	if err != nil {
		return nil, err
	}
	repo := sqlite.NewPortfolioRepo(db)
	svc := portfolio.NewService(repo)
	if h.quoter != nil {
		svc.SetNameResolver(func(ctx context.Context, code string) string {
			quotes, err := h.quoter.Quote(ctx, []string{code})
			if err != nil || quotes == nil {
				return ""
			}
			if q, ok := quotes[code]; ok {
				return q.StockName
			}
			return ""
		})
	}
	return svc, nil
}

// ---- Holdings ----

func (h *PortfolioHandler) ListHoldings(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	// 支持 ?account_id=N 筛选（0 或不传 = 全部）
	var holdings []portfolio.Holding
	if aidStr := c.Query("account_id"); aidStr != "" {
		aid, err2 := strconv.ParseInt(aidStr, 10, 64)
		if err2 != nil {
			WriteError(c, http.StatusBadRequest, "invalid account_id")
			return
		}
		holdings, err = svc.ListHoldingsByAccount(c.Request.Context(), aid)
	} else {
		holdings, err = svc.ListHoldings(c.Request.Context())
	}
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, holdings)
}

func (h *PortfolioHandler) Realized(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	results, err := svc.Realized(c.Request.Context())
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, results)
}

// ---- Actions ----

func (h *PortfolioHandler) ListActions(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	code := c.Param("code")
	actions, err := svc.ListActions(c.Request.Context(), code)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, actions)
}

type createActionReq struct {
	ActionType string   `json:"action_type" binding:"required"`
	Price      float64  `json:"price"`
	Shares     int64    `json:"shares" binding:"required"`
	Note       string   `json:"note"`
	TradeDate  string   `json:"trade_date"`
	TradeTime  string   `json:"trade_time"`
	Fee        *float64 `json:"fee"`
	Broker     string   `json:"broker"`
	AccountID  *int64   `json:"account_id,omitempty"`
}

func (h *PortfolioHandler) CreateAction(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	code := c.Param("code")
	var req createActionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	a := &portfolio.Action{
		StockCode:  code,
		ActionType: portfolio.ActionType(req.ActionType),
		Price:      req.Price,
		Shares:     req.Shares,
		Note:       req.Note,
		TradeDate:  req.TradeDate,
		TradeTime:  req.TradeTime,
		Fee:        req.Fee,
		Broker:     req.Broker,
		AccountID:  req.AccountID,
	}
	id, err := svc.CreateAction(c.Request.Context(), a)
	if err != nil {
		writePortfolioError(c, err)
		return
	}
	WriteJSON(c, http.StatusCreated, gin.H{"id": id})
}

func (h *PortfolioHandler) UpdateAction(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		WriteError(c, http.StatusBadRequest, "invalid id")
		return
	}
	var req createActionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	a := &portfolio.Action{
		ID:         id,
		StockCode:  "", // UpdateAction 内部不需要 code（从 DB 取）
		ActionType: portfolio.ActionType(req.ActionType),
		Price:      req.Price,
		Shares:     req.Shares,
		Note:       req.Note,
		TradeDate:  req.TradeDate,
		TradeTime:  req.TradeTime,
		Fee:        req.Fee,
		Broker:     req.Broker,
	}
	// 需要先查出 stock_code 用于重算——走主键索引，不做全表扫描。
	existing, err := svc.GetAction(c.Request.Context(), id)
	if err != nil {
		writePortfolioError(c, err)
		return
	}
	if existing == nil {
		WriteError(c, http.StatusNotFound, "action not found")
		return
	}
	a.StockCode = existing.StockCode
	if err := svc.UpdateAction(c.Request.Context(), a); err != nil {
		writePortfolioError(c, err)
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}

func (h *PortfolioHandler) DeleteAction(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		WriteError(c, http.StatusBadRequest, "invalid id")
		return
	}
	if err := svc.DeleteAction(c.Request.Context(), id); err != nil {
		writePortfolioError(c, err)
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}

// ---- Thesis ----

func (h *PortfolioHandler) ListThesis(c *gin.Context) {
	// 原版没有 list thesis 端点，前端通过 /api/portfolio/thesis/{code} 逐个查。
	// 这里返回空列表占位，后续可扩展。
	WriteJSON(c, http.StatusOK, []any{})
}

func (h *PortfolioHandler) GetThesis(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	code := c.Param("code")
	t, err := svc.GetThesis(c.Request.Context(), code)
	if err != nil {
		writePortfolioError(c, err)
		return
	}
	WriteJSON(c, http.StatusOK, t)
}

type upsertThesisReq struct {
	Name   string `json:"name"`
	Thesis string `json:"thesis" binding:"required"`
}

func (h *PortfolioHandler) UpsertThesis(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	code := c.Param("code")
	var req upsertThesisReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "thesis is required")
		return
	}
	t := &portfolio.Thesis{
		Code:   code,
		Name:   req.Name,
		Thesis: req.Thesis,
	}
	if err := svc.UpsertThesis(c.Request.Context(), t); err != nil {
		writePortfolioError(c, err)
		return
	}
	WriteJSON(c, http.StatusOK, t)
}

func (h *PortfolioHandler) DeleteThesis(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	code := c.Param("code")
	if err := svc.DeleteThesis(c.Request.Context(), code); err != nil {
		writePortfolioError(c, err)
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}

// ---- Watchlist ----

func (h *PortfolioHandler) ListWatchlist(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	items, err := svc.ListWatchlist(c.Request.Context())
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, items)
}

type addWatchlistReq struct {
	StockCode  string   `json:"stock_code" binding:"required"`
	StockName  string   `json:"stock_name"`
	AddedPrice *float64 `json:"added_price"`
}

func (h *PortfolioHandler) AddWatchlist(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	var req addWatchlistReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "stock_code is required")
		return
	}
	w := &portfolio.WatchlistItem{
		StockCode:  req.StockCode,
		StockName:  req.StockName,
		AddedPrice: req.AddedPrice,
	}
	if err := svc.AddWatchlist(c.Request.Context(), w); err != nil {
		writePortfolioError(c, err)
		return
	}
	WriteJSON(c, http.StatusCreated, gin.H{"ok": true})
}

func (h *PortfolioHandler) RemoveWatchlist(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	code := c.Param("code")
	if err := svc.RemoveWatchlist(c.Request.Context(), code); err != nil {
		writePortfolioError(c, err)
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}

// ---- Brokers ----

func (h *PortfolioHandler) ListBrokers(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	brokers, err := svc.ListBrokers(c.Request.Context())
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, brokers)
}

type createBrokerReq struct {
	Name      string  `json:"name" binding:"required"`
	StockRate float64 `json:"stock_rate"`
	StockMin  float64 `json:"stock_min"`
	EtfRate   float64 `json:"etf_rate"`
	EtfMin    float64 `json:"etf_min"`
	IsDefault bool    `json:"is_default"`
}

func (h *PortfolioHandler) CreateBroker(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	var req createBrokerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "name is required")
		return
	}
	b := &portfolio.Broker{
		Name:      req.Name,
		StockRate: req.StockRate,
		StockMin:  req.StockMin,
		EtfRate:   req.EtfRate,
		EtfMin:    req.EtfMin,
		IsDefault: req.IsDefault,
	}
	id, err := svc.CreateBroker(c.Request.Context(), b)
	if err != nil {
		writePortfolioError(c, err)
		return
	}
	WriteJSON(c, http.StatusCreated, gin.H{"id": id})
}

func (h *PortfolioHandler) UpdateBroker(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		WriteError(c, http.StatusBadRequest, "invalid id")
		return
	}
	var req createBrokerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "name is required")
		return
	}
	b := &portfolio.Broker{
		ID:        id,
		Name:      req.Name,
		StockRate: req.StockRate,
		StockMin:  req.StockMin,
		EtfRate:   req.EtfRate,
		EtfMin:    req.EtfMin,
		IsDefault: req.IsDefault,
	}
	if err := svc.UpdateBroker(c.Request.Context(), b); err != nil {
		writePortfolioError(c, err)
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}

func (h *PortfolioHandler) DeleteBroker(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		WriteError(c, http.StatusBadRequest, "invalid id")
		return
	}
	if err := svc.DeleteBroker(c.Request.Context(), id); err != nil {
		writePortfolioError(c, err)
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}

// ---- Accounts ----

func (h *PortfolioHandler) ListAccounts(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	accounts, err := svc.ListAccounts(c.Request.Context())
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, accounts)
}

type createAccountReq struct {
	Name      string              `json:"name" binding:"required"`
	Kind      portfolio.AccountKind `json:"kind"`
	Color     string              `json:"color"`
	SortOrder int                 `json:"sort_order"`
}

func (h *PortfolioHandler) CreateAccount(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	var req createAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "name is required")
		return
	}
	a := &portfolio.Account{
		Name:      req.Name,
		Kind:      req.Kind,
		Color:     req.Color,
		SortOrder: req.SortOrder,
	}
	id, err := svc.CreateAccount(c.Request.Context(), a)
	if err != nil {
		writePortfolioError(c, err)
		return
	}
	WriteJSON(c, http.StatusCreated, gin.H{"id": id})
}

func (h *PortfolioHandler) UpdateAccount(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		WriteError(c, http.StatusBadRequest, "invalid id")
		return
	}
	var req createAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "name is required")
		return
	}
	a := &portfolio.Account{
		ID:        id,
		Name:      req.Name,
		Kind:      req.Kind,
		Color:     req.Color,
		SortOrder: req.SortOrder,
	}
	if err := svc.UpdateAccount(c.Request.Context(), a); err != nil {
		writePortfolioError(c, err)
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}

func (h *PortfolioHandler) DeleteAccount(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		WriteError(c, http.StatusBadRequest, "invalid id")
		return
	}
	if err := svc.DeleteAccount(c.Request.Context(), id); err != nil {
		writePortfolioError(c, err)
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}

func (h *PortfolioHandler) AccountSummaries(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	summaries, err := svc.AccountSummaries(c.Request.Context())
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, summaries)
}

// ---- error mapping ----

func writePortfolioError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, portfolio.ErrNotFound):
		WriteError(c, http.StatusNotFound, "not found")
	case errors.Is(err, portfolio.ErrInvalidCode),
		errors.Is(err, portfolio.ErrInvalidAction),
		errors.Is(err, portfolio.ErrInvalidPrice),
		errors.Is(err, portfolio.ErrInvalidShares):
		WriteError(c, http.StatusBadRequest, err.Error())
	case errors.Is(err, portfolio.ErrOversell):
		WriteError(c, http.StatusConflict, err.Error())
	case errors.Is(err, portfolio.ErrDuplicateBroker):
		WriteError(c, http.StatusConflict, err.Error())
	case errors.Is(err, portfolio.ErrDuplicateName):
		WriteError(c, http.StatusConflict, err.Error())
	case errors.Is(err, portfolio.ErrBrokerInUse):
		WriteError(c, http.StatusConflict, err.Error())
	default:
		WriteError(c, http.StatusInternalServerError, err.Error())
	}
}

// ---- Dividend events ----

func (h *PortfolioHandler) ListDividendEvents(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	events, err := svc.ListDividendEvents(c.Request.Context(), c.Param("code"))
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, events)
}

type upsertDividendReq struct {
	ExDate       string  `json:"ex_date" binding:"required"`
	CashPerShare float64 `json:"cash_per_share"`
	BonusRatio   float64 `json:"bonus_ratio"`
	Source       string  `json:"source"`
	Note         string  `json:"note"`
}

func (h *PortfolioHandler) UpsertDividendEvent(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	var req upsertDividendReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	e := &portfolio.DividendEvent{
		StockCode:    c.Param("code"),
		ExDate:       req.ExDate,
		CashPerShare: req.CashPerShare,
		BonusRatio:   req.BonusRatio,
		Source:       req.Source,
		Note:         req.Note,
	}
	id, err := svc.UpsertDividendEvent(c.Request.Context(), e)
	if err != nil {
		WriteError(c, http.StatusBadRequest, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"id": id})
}

func (h *PortfolioHandler) DeleteDividendEvent(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		WriteError(c, http.StatusBadRequest, "invalid id")
		return
	}
	if err := svc.DeleteDividendEvent(c.Request.Context(), id); err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}

// Curve 返回组合净值曲线（TWR + 成本基线市值 + 沪深300基准 + 指标）。
// days 为轴长上限（默认 120，最大 500）。
func (h *PortfolioHandler) Curve(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	days, _ := strconv.Atoi(c.DefaultQuery("days", "120"))
	db, err := h.dbFn(auth.MustUserID(c.Request.Context()))
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	ext := sqlite.NewExternalRepo(db)
	curve, err := svc.BuildCurve(c.Request.Context(), days, ext)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	if h.kline != nil {
		attachBenchmark(c.Request.Context(), curve, h.kline)
	}
	WriteJSON(c, http.StatusOK, curve)
}

// attachBenchmark 尝试叠加沪深300基准（归一 100）。网络不可达时静默跳过。
func attachBenchmark(ctx context.Context, curve *portfolio.Curve, kline market.KlineProvider) {
	const benchCode = "sh000300"
	kl, err := kline.Kline(ctx, benchCode, len(curve.Dates)+10)
	if err != nil || len(kl) < 2 {
		return
	}
	closeByDate := make(map[string]float64, len(kl))
	for _, k := range kl {
		closeByDate[k.Date] = k.Close
	}
	raw := make([]float64, len(curve.Dates))
	have := make([]bool, len(curve.Dates))
	var last float64
	got := false
	for i, d := range curve.Dates {
		if v, ok := closeByDate[d]; ok {
			last = v
			got = true
		}
		if got {
			raw[i] = last
			have[i] = true
		}
	}
	start := -1
	for i := range have {
		if have[i] {
			start = i
			break
		}
	}
	if start < 0 {
		return
	}
	b0 := raw[start]
	if b0 <= 0 {
		return
	}
	bench := make([]float64, len(curve.Dates))
	for i := 0; i < len(curve.Dates); i++ {
		if i < start || !have[i] {
			bench[i] = 100
			continue
		}
		bench[i] = raw[i] / b0 * 100
	}
	curve.Bench = bench
	curve.BenchName = "沪深300"
	if have[len(curve.Dates)-1] {
		bre := raw[len(curve.Dates)-1]/b0*100 - 100
		curve.Metrics.BenchReturnPct = &bre
		ex := curve.Metrics.ReturnPct - bre
		curve.Metrics.ExcessPct = &ex
	}
}
