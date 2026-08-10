package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// ─────────────────────────── resolve_stock ───────────────────────────
// 把股票名字或代码解析成标准代码+名称。用户报名字(如'中钨高新')时先调它拿代码。

type resolveStockArgs struct {
	Query string `json:"query"`
}

func (s *Service) toolResolveStock() (tool.InvokableTool, error) {
	info := &schema.ToolInfo{
		Name: "resolve_stock",
		Desc: "把股票名字或代码解析成标准代码+名称。用户报名字(如'中钨高新')或行业词时先调它拿代码。" +
			"A股代码用裸6位(600667/000657)，不要加 sh/sz 前缀；港股 HK.00700；美股 US.AAPL。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query": strParam("股票名字或代码", true),
		}),
	}
	return defTool[*resolveStockArgs, string](info, func(ctx context.Context, a *resolveStockArgs) (string, error) {
		q := strings.TrimSpace(a.Query)
		if q == "" {
			return "请提供股票名字或代码", nil
		}
		// 1) 已经是 6 位纯数字 → 当作 A 股裸代码，查一次行情确认名称
		if isBareCode(q) {
			if q, err := s.normalizeCode(ctx, q); err == nil {
				return q, nil
			}
		}
		// 2) 尝试当作代码直接查
		if code, err := s.normalizeCode(ctx, q); err == nil {
			return code, nil
		}
		// 3) 名字 → 尝试在持仓/行情里匹配（轻量：先查行情接口按名）
		// 简化实现：直接返回无法解析，引导模型用代码提问。
		return fmt.Sprintf("无法将「%s」解析为标准代码，请直接提供股票代码（如 600519）", q), nil
	})
}

func (s *Service) normalizeCode(ctx context.Context, code string) (string, error) {
	c := normalizeBareCode(code)
	quotes, err := s.quoter.Quote(ctx, []string{c})
	if err != nil {
		return "", err
	}
	q, ok := quotes[c]
	if !ok || q.StockName == "" {
		return "", fmt.Errorf("no quote")
	}
	return fmt.Sprintf("%s(%s)", q.StockName, c), nil
}

func isBareCode(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// normalizeBareCode 把可能带 sh/sz 前缀的代码归一化为裸6位。
func normalizeBareCode(code string) string {
	code = strings.TrimSpace(code)
	code = strings.TrimPrefix(code, "sh")
	code = strings.TrimPrefix(code, "sz")
	return code
}

// ─────────────────────────── get_quote ───────────────────────────
// 查个股实时行情: 现价/当日涨跌幅/开高低/成交额/换手。

type getQuoteArgs struct {
	Code string `json:"code"`
}

func (s *Service) toolGetQuote() (tool.InvokableTool, error) {
	info := &schema.ToolInfo{
		Name: "get_quote",
		Desc: "查个股实时行情: 现价/当日涨跌幅/开高低/成交额/换手。code 用 resolve_stock 返回的裸6位代码(A股)或 HK.00700/US.AAPL。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"code": strParam("股票代码（A股裸6位/港股HK.xxx/美股US.xxx）", true),
		}),
	}
	return defTool[*getQuoteArgs, string](info, func(ctx context.Context, a *getQuoteArgs) (string, error) {
		code := normalizeBareCode(a.Code)
		quotes, err := s.quoter.Quote(ctx, []string{code})
		if err != nil {
			return "", err
		}
		q, ok := quotes[code]
		if !ok {
			return fmt.Sprintf("未找到 %s 的行情数据", code), nil
		}
		return fmt.Sprintf("%s(%s) 现价%.2f 涨跌%.2f%% 开%.2f 高%.2f 低%.2f 昨收%.2f 成交额%.0f万 振幅%.2f%%",
			q.StockName, q.StockCode, q.Price, q.ChangePct, q.Open, q.High, q.Low, q.PrevClose,
			q.Amount/1e4, q.Amplitude), nil
	})
}

// ─────────────────────────── get_trend ───────────────────────────
// 查个股近 N 个交易日走势(裸K + 量): 累计涨跌/逐日涨跌/上涨天数。

type getTrendArgs struct {
	Code string `json:"code"`
	Days int    `json:"days"`
}

func (s *Service) toolGetTrend() (tool.InvokableTool, error) {
	info := &schema.ToolInfo{
		Name: "get_trend",
		Desc: "查个股近 N 个交易日走势: 累计涨跌/逐日涨跌/上涨天数/量能。支持 A股/港股/美股。days 默认20。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"code": strParam("股票代码（A股裸6位/港股HK.xxx/美股US.xxx）", true),
			"days": intParam("交易日数，默认20", false),
		}),
	}
	return defTool[*getTrendArgs, string](info, func(ctx context.Context, a *getTrendArgs) (string, error) {
		code := normalizeBareCode(a.Code)
		days := a.Days
		if days <= 0 {
			days = 20
		}
		kl, err := s.kliner.Kline(ctx, code, days)
		if err != nil {
			return "", err
		}
		if len(kl) == 0 {
			return fmt.Sprintf("%s 无历史K线数据", code), nil
		}
		var sb strings.Builder
		var up, down int
		var cum float64
		type row struct {
			Date string  `json:"date"`
			Pct  float64 `json:"pct"`
			Vol  float64 `json:"vol"`
		}
		rows := make([]row, 0, len(kl))
		for i, k := range kl {
			var pct float64
			// KlineDay 无 PrevClose 字段，用上一交易日收盘推算当日涨跌。
			if i > 0 && kl[i-1].Close > 0 {
				pct = (k.Close - kl[i-1].Close) / kl[i-1].Close * 100
			}
			rows = append(rows, row{Date: k.Date, Pct: round1(pct), Vol: k.Volume})
			cum += pct
			if pct >= 0 {
				up++
			} else {
				down++
			}
		}
		last := kl[len(kl)-1]
		fmt.Fprintf(&sb, "%s 近%d日: 累计%s%.2f%% 上涨%d天/下跌%d天 最新收%.2f\n",
			code, days, sign(cum), cum, up, down, last.Close)
		// 只回传最近 12 条逐日，避免撑爆上下文
		start := 0
		if len(rows) > 12 {
			start = len(rows) - 12
		}
		b, _ := json.Marshal(rows[start:])
		sb.Write(b)
		return sb.String(), nil
	})
}

// ─────────────────────────── get_chain_quote ───────────────────────────
// 批量取一组票的多周期量价摘要(产业链全景/多票横向对比专用)。

type getChainQuoteArgs struct {
	Stocks []string `json:"stocks"`
}

func (s *Service) toolGetChainQuote() (tool.InvokableTool, error) {
	info := &schema.ToolInfo{
		Name: "get_chain_quote",
		Desc: "批量取一组票的多周期量价摘要(产业链全景/多票横向对比): 一次返回每只的 pct_5d/pct_20d/pct_60d 涨幅、距20日高、量能。最多24只。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"stocks": arrStrParam("股票名称或代码列表，最多24只", true),
		}),
	}
	return defTool[*getChainQuoteArgs, string](info, func(ctx context.Context, a *getChainQuoteArgs) (string, error) {
		if len(a.Stocks) == 0 {
			return "stocks 为空", nil
		}
		codes := make([]string, 0, len(a.Stocks))
		for _, st := range a.Stocks {
			codes = append(codes, normalizeBareCode(st))
		}
		quotes, err := s.quoter.Quote(ctx, codes)
		if err != nil {
			return "", err
		}
		type rec struct {
			Code  string  `json:"code"`
			Name  string  `json:"name"`
			Price float64 `json:"price"`
			Pct   float64 `json:"pct"`
		}
		out := make([]rec, 0, len(codes))
		for _, c := range codes {
			if q, ok := quotes[c]; ok {
				out = append(out, rec{Code: c, Name: q.StockName, Price: q.Price, Pct: round1(q.ChangePct)})
			}
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	})
}

// ─────────────────────────── get_fund_quote ───────────────────────────
// 查基金净值。

type getFundQuoteArgs struct {
	Codes []string `json:"codes"`
}

func (s *Service) toolGetFundQuote() (tool.InvokableTool, error) {
	info := &schema.ToolInfo{
		Name: "get_fund_quote",
		Desc: "查基金最新净值(单位净值/累计净值/涨跌/日期)。codes 为6位基金代码，如 110011。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"codes": arrStrParam("基金代码列表，如 110011", true),
		}),
	}
	return defTool[*getFundQuoteArgs, string](info, func(ctx context.Context, a *getFundQuoteArgs) (string, error) {
		if s.fundQ == nil || len(a.Codes) == 0 {
			return "未配置基金净值源或无代码", nil
		}
		qs, err := s.fundQ.QuoteFunds(ctx, a.Codes)
		if err != nil {
			return "", err
		}
		type rec struct {
			Code  string  `json:"code"`
			Name  string  `json:"name"`
			NAV   float64 `json:"unit_nav"`
			Cum   float64 `json:"cum_nav"`
			Pct   float64 `json:"change_pct"`
			Date  string  `json:"date"`
		}
		out := make([]rec, 0, len(a.Codes))
		for _, c := range a.Codes {
			if q, ok := qs[c]; ok {
				out = append(out, rec{Code: c, Name: q.Name, NAV: q.UnitNAV, Cum: q.CumNAV, Pct: round1(q.ChangePct), Date: q.Date})
			}
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	})
}

// ── 小工具 ──
func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}
func sign(f float64) string {
	if f >= 0 {
		return "+"
	}
	return ""
}
