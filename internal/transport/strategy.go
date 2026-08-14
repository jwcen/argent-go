package transport

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/auth"
	"github.com/jwcen/argent-go/internal/infra/sqlite"
	"github.com/jwcen/argent-go/internal/market"
	"github.com/jwcen/argent-go/internal/portfolio"
	"github.com/jwcen/argent-go/internal/strategy"
)

// StrategyHandler 把「诚实版」策略能力暴露为 HTTP 接口。
//
// 与 PortfolioHandler 同理：portfolio 数据在每用户独立库，
// 故 svc 按请求构造；K 线来自全局行情源（market.KlineProvider）。
type StrategyHandler struct {
	dbFn   func(userID int64) (*sql.DB, error)
	kline  market.KlineProvider
	quoter market.Quoter
}

func NewStrategyHandler(dbFn func(userID int64) (*sql.DB, error), kline market.KlineProvider, quoter market.Quoter) *StrategyHandler {
	return &StrategyHandler{dbFn: dbFn, kline: kline, quoter: quoter}
}

// Register 挂载 /api/strategy 下的路由（需挂在 RequireAuth 分组上）。
func (h *StrategyHandler) Register(r gin.IRouter) {
	g := r.Group("/strategy")
	g.GET("", h.List)              // 所有 A 股持仓的策略报告（策略栏数据源）
	g.GET("/:code", h.One)         // 单只报告（可选账本复盘）
	g.POST("/backtest", h.Backtest) // 对任意代码做均线择时回测
}

func (h *StrategyHandler) svc(c *gin.Context) (*portfolio.Service, error) {
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
			q, err := h.quoter.Quote(ctx, []string{code})
			if err != nil || q == nil {
				return ""
			}
			if x, ok := q[code]; ok {
				return x.StockName
			}
			return ""
		})
	}
	return svc, nil
}

// List 返回当前用户所有 A 股持仓的策略报告（中性参考 + 账本复盘）。
func (h *StrategyHandler) List(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	ctx := c.Request.Context()
	holdings, err := svc.ListHoldings(ctx)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	reports := make([]*strategy.Report, 0, len(holdings))
	for _, hd := range holdings {
		rep := h.buildReport(ctx, svc, hd.StockCode, hd.StockName, hd.CostPrice, hd.Shares)
		if rep != nil {
			reports = append(reports, rep)
		}
	}
	WriteJSON(c, http.StatusOK, gin.H{"items": reports})
}

// One 返回单只报告；若该代码在用户账本中，则附带决策复盘。
func (h *StrategyHandler) One(c *gin.Context) {
	code := c.Param("code")
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	ctx := c.Request.Context()
	name := code
	var cost float64
	var shares int64
	if hds, e := svc.ListHoldings(ctx); e == nil {
		for _, hd := range hds {
			if hd.StockCode == code {
				name = hd.StockName
				cost = hd.CostPrice
				shares = hd.Shares
				break
			}
		}
	}
	rep := h.buildReport(ctx, svc, code, name, cost, shares)
	if rep == nil {
		WriteError(c, http.StatusBadGateway, "无法获取 K 线数据")
		return
	}
	WriteJSON(c, http.StatusOK, rep)
}

// Backtest 对任意 A 股代码用指定均线策略做历史回测。
func (h *StrategyHandler) Backtest(c *gin.Context) {
	var req struct {
		strategy.BacktestParams
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	code := req.Code
	if !market.IsAShare(code) {
		WriteError(c, http.StatusBadRequest, "code 需为 6 位 A 股代码")
		return
	}

	ctx := c.Request.Context()
	klines, err := h.kline.Kline(ctx, code, 1500)
	if err != nil || len(klines) < 60 {
		WriteError(c, http.StatusBadGateway, "无法获取该代码的历史 K 线（需要网络）")
		return
	}
	name := req.Name
	if name == "" {
		if q, e := h.quoter.Quote(ctx, []string{code}); e == nil && q != nil {
			if x, ok := q[code]; ok {
				name = x.StockName
			}
		}
	}
	rep, err := strategy.RunBacktest(code, name, klines, req.BacktestParams)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, rep)
}

// buildReport 组装单只报告：拉 K 线算指标 + 从账本算决策复盘。
// K 线不可用时返回 nil（调用方据此跳过或报错）。
func (h *StrategyHandler) buildReport(ctx context.Context, svc *portfolio.Service, code, name string, cost float64, shares int64) *strategy.Report {
	klines, err := h.kline.Kline(ctx, code, 400)
	if err != nil || len(klines) < 2 {
		return nil
	}

	var review *strategy.DecisionReviewInput
	if shares > 0 {
		review = h.decisionReview(ctx, svc, code, cost, shares)
	}

	if len(klines) < 2 {
		return nil
	}
	rep, err := strategy.Evaluate(code, name, klines, review)
	if err != nil {
		return nil
	}
	return rep
}

// decisionReview 读流水找最早 BUY，算建仓日期与持有天数，组装复盘输入。
func (h *StrategyHandler) decisionReview(ctx context.Context, svc *portfolio.Service, code string, cost float64, shares int64) *strategy.DecisionReviewInput {
	actions, err := svc.ListActions(ctx, code)
	if err != nil || len(actions) == 0 {
		return &strategy.DecisionReviewInput{FirstBuyDate: "", HoldingDays: 0, CostPrice: cost, Shares: shares}
	}
	var firstBuy string
	for _, a := range actions {
		if a.ActionType == portfolio.ActionBuy {
			if firstBuy == "" || a.TradeDate < firstBuy {
				firstBuy = a.TradeDate
			}
		}
	}
	days := 0
	if firstBuy != "" {
		if t, e := time.Parse("2006-01-02", firstBuy); e == nil {
			days = int(time.Since(t).Hours() / 24)
			if days < 0 {
				days = 0
			}
		}
	}
	return &strategy.DecisionReviewInput{
		FirstBuyDate: firstBuy,
		HoldingDays:  days,
		CostPrice:    cost,
		Shares:       shares,
	}
}
