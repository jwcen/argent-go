package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/agent"
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
// analyzer 可选：注入后提供结构化 AI 个股分析（/strategy/:code/analysis）。
type StrategyHandler struct {
	dbFn     func(userID int64) (*sql.DB, error)
	kline    market.KlineProvider
	quoter   market.Quoter
	analyzer *agent.Service
}

func NewStrategyHandler(dbFn func(userID int64) (*sql.DB, error), kline market.KlineProvider, quoter market.Quoter) *StrategyHandler {
	return &StrategyHandler{dbFn: dbFn, kline: kline, quoter: quoter}
}

// SetAnalyzer 注入 LLM 分析服务（可选；不注入则 /analysis 返回 503）。
func (h *StrategyHandler) SetAnalyzer(a *agent.Service) {
	h.analyzer = a
}

// Register 挂载 /api/strategy 下的路由（需挂在 RequireAuth 分组上）。
func (h *StrategyHandler) Register(r gin.IRouter) {
	g := r.Group("/strategy")
	g.GET("", h.List)              // 所有 A 股持仓的策略报告（策略栏数据源）
	g.GET("/:code", h.One)         // 单只报告（可选账本复盘）
	g.GET("/:code/detail", h.Detail)     // 单只技术面明细（K线+指标序列+支撑压力）
	g.POST("/:code/analysis", h.Analysis) // 结构化 AI 分析（方向/建议/触发/风险）
	g.POST("/backtest", h.Backtest) // 对任意代码做策略回测
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

// GET /api/strategy/:code/detail — 技术面明细（K线 + 指标序列 + 支撑压力）。
func (h *StrategyHandler) Detail(c *gin.Context) {
	code := c.Param("code")
	if !market.IsAShare(code) {
		WriteError(c, http.StatusBadRequest, "code 需为 6 位 A 股代码")
		return
	}
	ctx := c.Request.Context()
	klines, err := h.kline.Kline(ctx, code, 400)
	if err != nil || len(klines) < 30 {
		WriteError(c, http.StatusBadGateway, "无法获取历史 K 线（需要网络）")
		return
	}
	d, err := strategy.AnalyzeTechnical(code, h.resolveName(ctx, code), klines)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, d)
}

// analysisResp 结构化 AI 分析结果（对齐卧龙 /ai/analysis 的四段式）。
type analysisResp struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Direction string `json:"direction"` // 看多/看空/中性 + 理由
	Advice    string `json:"advice"`    // 买入区间/止损/目标/仓位
	Trigger   string `json:"trigger"`   // 执行触发条件
	Risk      string `json:"risk"`      // 风险提示
	Raw       string `json:"raw,omitempty"` // 解析失败时的模型原文兜底
}

// POST /api/strategy/:code/analysis — 结构化 AI 分析（方向/建议/触发/风险）。
func (h *StrategyHandler) Analysis(c *gin.Context) {
	if h.analyzer == nil || !h.analyzer.IsConfigured() {
		WriteError(c, http.StatusServiceUnavailable, "LLM not configured")
		return
	}
	code := c.Param("code")
	if !market.IsAShare(code) {
		WriteError(c, http.StatusBadRequest, "code 需为 6 位 A 股代码")
		return
	}
	ctx := c.Request.Context()
	klines, err := h.kline.Kline(ctx, code, 250)
	if err != nil || len(klines) < 30 {
		WriteError(c, http.StatusBadGateway, "无法获取历史 K 线（需要网络）")
		return
	}
	name := h.resolveName(ctx, code)

	// 组装数据块：报价 + 支撑压力 + 指标快照
	var price, pct float64
	if h.quoter != nil {
		if q, e := h.quoter.Quote(ctx, []string{code}); e == nil && q != nil {
			if x, ok := q[code]; ok {
				price, pct = x.Price, x.ChangePct
			}
		}
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("股票：%s（%s）\n", name, code))
	sb.WriteString(fmt.Sprintf("现价：%.2f（涨跌 %.2f%%）\n", price, pct))
	if tech, e := strategy.AnalyzeTechnical(code, name, klines); e == nil {
		sb.WriteString(fmt.Sprintf("支撑：%.2f / 远端 %.2f；压力：%.2f / 远端 %.2f\n",
			tech.Support, tech.SupportFar, tech.Resistance, tech.ResistanceFar))
	}
	if rep, e := strategy.Evaluate(code, name, klines, nil); e == nil {
		sb.WriteString(fmt.Sprintf("MA20 %.2f / MA60 %.2f；MACD DIF %.3f / DEA %.3f；RSI %.1f；趋势：%s\n",
			rep.Indicators.MA20, rep.Indicators.MA60,
			rep.Indicators.MACD.DIF, rep.Indicators.MACD.DEA,
			rep.Indicators.RSI, rep.Trend))
	}

	systemPrompt := "你是一位专业的 A 股技术分析师。请基于给定的行情数据与指标输出结构化分析。" +
		"必须只输出一个 JSON 对象（不要任何多余文字），字段如下，全部用中文：" +
		`{"direction":"看多/看空/中性判断，并简述理由（趋势、资金、预期、基本面）",` +
		`"advice":"交易建议：买入区间、止损位、目标位、仓位建议",` +
		`"trigger":"执行触发条件：哪些条件成立时更适合买入或卖出",` +
		`"risk":"风险提示：潜在风险、催化因素、质量验证"}`
	userPrompt := sb.String() + "请分析这只股票。"

	answer, err := h.analyzer.ChatText(ctx, systemPrompt, userPrompt)
	if err != nil {
		WriteError(c, http.StatusBadGateway, "AI 分析失败："+err.Error())
		return
	}
	resp := parseAnalysis(code, name, answer)
	WriteJSON(c, http.StatusOK, resp)
}

// parseAnalysis 把模型输出解析成四段式；解析失败时把原文塞进 Raw 兜底。
func parseAnalysis(code, name, raw string) analysisResp {
	r := analysisResp{Code: code, Name: name}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		var m map[string]string
		if err := json.Unmarshal([]byte(raw[start:end+1]), &m); err == nil {
			r.Direction = m["direction"]
			r.Advice = m["advice"]
			r.Trigger = m["trigger"]
			r.Risk = m["risk"]
			if r.Direction != "" || r.Advice != "" || r.Trigger != "" || r.Risk != "" {
				return r
			}
		}
	}
	r.Raw = raw
	return r
}

// resolveName 用报价源解析股票名，失败时回退为代码本身。
func (h *StrategyHandler) resolveName(ctx context.Context, code string) string {
	if h.quoter == nil {
		return code
	}
	if q, e := h.quoter.Quote(ctx, []string{code}); e == nil && q != nil {
		if x, ok := q[code]; ok && x.StockName != "" {
			return x.StockName
		}
	}
	return code
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
