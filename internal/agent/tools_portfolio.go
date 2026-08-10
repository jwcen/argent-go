package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// ─────────────────────────── get_holdings ───────────────────────────
// 查用户当前全部在持: A股 + 场外资产(基金/ETF/理财/现金)。

type emptyArgs struct{}

func (s *Service) toolGetHoldings() (tool.InvokableTool, error) {
	info := &schema.ToolInfo{
		Name:        "get_holdings",
		Desc:        "查用户当前全部在持: A股(代码/名称/股数/综合成本/持有天数) + 场外资产(基金/ETF/理财/现金)。回答'我的持仓/我有什么/我持有啥'时用。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}
	return defTool[*emptyArgs, string](info, func(ctx context.Context, _ *emptyArgs) (string, error) {
		us := userServicesFromCtx(ctx)
		if us == nil || us.portfolio == nil {
			return "", errNoUser
		}
		hs, err := us.portfolio.ListHoldings(ctx)
		if err != nil {
			return "", err
		}
		stocks := make([]map[string]any, 0, len(hs))
		for _, h := range hs {
			stocks = append(stocks, map[string]any{
				"code": h.StockCode, "name": h.StockName, "shares": h.Shares,
				"cost_price": h.CostPrice, "hold_days": h.WeightedDays,
			})
		}
		var funds []map[string]any
		if us.external != nil {
			assets, e := us.external.ListAssets(ctx)
			if e == nil {
				for _, a := range assets {
					funds = append(funds, map[string]any{
						"type": a.AssetType, "name": a.Name, "cost_amount": a.CostAmount, "shares": a.Shares,
					})
				}
			}
		}
		out := map[string]any{"a_stocks": stocks, "external_assets": funds, "a_stock_count": len(stocks), "external_count": len(funds)}
		b, _ := json.Marshal(out)
		return string(b), nil
	})
}

// ─────────────────────────── get_asset_allocation ───────────────────────────
// 查用户全量资产配置: 各大类市值+占比 + 现金与理财逐笔明细。

func (s *Service) toolGetAssetAllocation() (tool.InvokableTool, error) {
	info := &schema.ToolInfo{
		Name:        "get_asset_allocation",
		Desc:        "查用户全量资产配置: 各大类(股票/现金/理财/基金/加密/机器人)市值+占比 + 现金与理财逐笔明细。回答'现金/理财怎么分配、应急金够不够、资产结构合不合理'时用。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}
	return defTool[*emptyArgs, string](info, func(ctx context.Context, _ *emptyArgs) (string, error) {
		us := userServicesFromCtx(ctx)
		if us == nil || us.external == nil {
			return "", errNoUser
		}
		assets, err := us.external.ListAssets(ctx)
		if err != nil {
			return "", err
		}
		byType := map[string]float64{}
		var details []map[string]any
		for _, a := range assets {
			byType[a.AssetType] += a.CostAmount
			if a.AssetType == "cash" || a.AssetType == "wealth" {
				details = append(details, map[string]any{"type": a.AssetType, "name": a.Name, "amount": a.CostAmount})
			}
		}
		out := map[string]any{"by_type": byType, "cash_wealth_details": details}
		b, _ := json.Marshal(out)
		return string(b), nil
	})
}

// ─────────────────────────── get_trades ───────────────────────────
// 查用户成交记录(含个股/场外基金)。

type getTradesArgs struct {
	Code  string `json:"code"`
	Start string `json:"start"`
	End   string `json:"end"`
}

func (s *Service) toolGetTrades() (tool.InvokableTool, error) {
	info := &schema.ToolInfo{
		Name: "get_trades",
		Desc: "查用户成交记录: 传 code→该标的买卖流水(A股含综合成本/已实现盈亏); 不传→最近全部成交。可用 start/end(YYYY-MM-DD)筛区间。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"code":  strParam("可选, 6位代码; 留空看全部", false),
			"start": strParam("可选, 起始日 YYYY-MM-DD", false),
			"end":   strParam("可选, 截止日 YYYY-MM-DD", false),
		}),
	}
	return defTool[*getTradesArgs, string](info, func(ctx context.Context, a *getTradesArgs) (string, error) {
		us := userServicesFromCtx(ctx)
		if us == nil || us.portfolio == nil {
			return "", errNoUser
		}
		code := normalizeBareCode(a.Code)
		actions, err := us.portfolio.ListActions(ctx, code)
		if err != nil {
			return "", err
		}
		out := make([]map[string]any, 0, len(actions))
		for _, ac := range actions {
			if a.Start != "" && ac.TradeDate < a.Start {
				continue
			}
			if a.End != "" && ac.TradeDate > a.End {
				continue
			}
			out = append(out, map[string]any{
				"code": ac.StockCode, "type": ac.ActionType, "price": ac.Price,
				"shares": ac.Shares, "trade_date": ac.TradeDate, "note": ac.Note,
			})
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	})
}

// ─────────────────────────── get_thesis ───────────────────────────
// 读用户当初记录的买入逻辑。

type getThesisArgs struct {
	Code string `json:"code"`
}

func (s *Service) toolGetThesis() (tool.InvokableTool, error) {
	info := &schema.ToolInfo{
		Name: "get_thesis",
		Desc: "读用户当初记录的买入逻辑(为什么买这只)。传 code 看单只; 不传看全部。对照现价/基本面/消息客观说每条理由还成不成立。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"code": strParam("可选, 6位代码; 留空看全部", false),
		}),
	}
	return defTool[*getThesisArgs, string](info, func(ctx context.Context, a *getThesisArgs) (string, error) {
		us := userServicesFromCtx(ctx)
		if us == nil || us.portfolio == nil {
			return "", errNoUser
		}
		code := normalizeBareCode(a.Code)
		if code == "" {
			hs, err := us.portfolio.ListHoldings(ctx)
			if err != nil {
				return "", err
			}
			out := map[string]string{}
			for _, h := range hs {
				t, e := us.portfolio.GetThesis(ctx, h.StockCode)
				if e == nil && t != nil {
					out[h.StockCode] = t.Thesis
				}
			}
			b, _ := json.Marshal(out)
			return string(b), nil
		}
		t, err := us.portfolio.GetThesis(ctx, code)
		if err != nil {
			return "", err
		}
		if t == nil {
			return fmt.Sprintf("%s 未记录买入逻辑", code), nil
		}
		return fmt.Sprintf("%s(%s): %s", code, t.Name, t.Thesis), nil
	})
}
