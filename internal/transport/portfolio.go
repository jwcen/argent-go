package transport

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/auth"
	"github.com/jwcen/argent-go/internal/infra/sqlite"
	"github.com/jwcen/argent-go/internal/portfolio"
)

// PortfolioHandler 把 portfolio.Service 适配成 HTTP 接口。
//
// 注意：portfolio 数据存在每用户独立库（users/u{id}.db）里，
// 所以不能在 bootstrap 时固定一个 repo——需要按请求获取当前用户的 DB。
// dbFn 就是这个「按 userID 取 *sql.DB」的函数，由 bootstrap 注入 store.Manager.User。
type PortfolioHandler struct {
	dbFn func(userID int64) (*sql.DB, error)
}

func NewPortfolioHandler(dbFn func(userID int64) (*sql.DB, error)) *PortfolioHandler {
	return &PortfolioHandler{dbFn: dbFn}
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

	wl := r.Group("/watchlist")
	wl.GET("", h.ListWatchlist)
	wl.POST("", h.AddWatchlist)
	wl.DELETE("/:code", h.RemoveWatchlist)

	b := r.Group("/brokers")
	b.GET("", h.ListBrokers)
	b.POST("", h.CreateBroker)
	b.PUT("/:id", h.UpdateBroker)
	b.DELETE("/:id", h.DeleteBroker)
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
	return portfolio.NewService(repo), nil
}

// ---- Holdings ----

func (h *PortfolioHandler) ListHoldings(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	holdings, err := svc.ListHoldings(c.Request.Context())
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
	case errors.Is(err, portfolio.ErrBrokerInUse):
		WriteError(c, http.StatusConflict, err.Error())
	default:
		WriteError(c, http.StatusInternalServerError, err.Error())
	}
}
