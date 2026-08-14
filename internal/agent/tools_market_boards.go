package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// ─────────────────────────── get_sector_momentum ───────────────────────────
// 行业板块涨跌榜（按涨跌幅降序），用于判断板块轮动/市场主线。

type sectorMomentumArgs struct {
	Limit int `json:"limit"`
}

func (s *Service) toolGetSectorMomentum() (tool.InvokableTool, error) {
	info := &schema.ToolInfo{
		Name: "get_sector_momentum",
		Desc: "查行业板块涨跌榜（按涨跌幅降序，含主力净流入），用于判断板块轮动/今日市场主线。回答'哪些板块涨/市场主线/板块轮动'时用。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"limit": intParam("返回前几名，默认15", false),
		}),
	}
	return defTool[*sectorMomentumArgs, string](info, func(ctx context.Context, a *sectorMomentumArgs) (string, error) {
		if s.sector == nil {
			return "未配置板块数据源", nil
		}
		limit := a.Limit
		if limit <= 0 {
			limit = 15
		}
		bs, err := s.sector.Sectors(ctx, "industry", limit)
		if err != nil {
			return "", err
		}
		if len(bs) == 0 {
			return "未获取到行业板块数据", nil
		}
		type rec struct {
			Code         string  `json:"code"`
			Name         string  `json:"name"`
			ChangePct    float64 `json:"change_pct"`
			MainNetInflow float64 `json:"main_net_inflow"`
		}
		out := make([]rec, 0, len(bs))
		for _, b := range bs {
			out = append(out, rec{Code: b.Code, Name: b.Name, ChangePct: b.ChangePct, MainNetInflow: b.MainNetInflow})
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	})
}

// ─────────────────────────── get_hot_concepts ───────────────────────────
// 概念板块涨跌榜，用于捕捉题材热点。

func (s *Service) toolGetHotConcepts() (tool.InvokableTool, error) {
	info := &schema.ToolInfo{
		Name: "get_hot_concepts",
		Desc: "查概念板块涨跌榜（按涨跌幅降序，含主力净流入），用于捕捉题材热点/资金主攻方向。回答'什么题材在炒/热点概念/题材热度'时用。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"limit": intParam("返回前几名，默认15", false),
		}),
	}
	return defTool[*sectorMomentumArgs, string](info, func(ctx context.Context, a *sectorMomentumArgs) (string, error) {
		if s.sector == nil {
			return "未配置板块数据源", nil
		}
		limit := a.Limit
		if limit <= 0 {
			limit = 15
		}
		bs, err := s.sector.Sectors(ctx, "concept", limit)
		if err != nil {
			return "", err
		}
		if len(bs) == 0 {
			return "未获取到概念板块数据", nil
		}
		type rec struct {
			Code          string  `json:"code"`
			Name          string  `json:"name"`
			ChangePct     float64 `json:"change_pct"`
			MainNetInflow float64 `json:"main_net_inflow"`
		}
		out := make([]rec, 0, len(bs))
		for _, b := range bs {
			out = append(out, rec{Code: b.Code, Name: b.Name, ChangePct: b.ChangePct, MainNetInflow: b.MainNetInflow})
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	})
}

// ─────────────────────────── get_board_stocks ───────────────────────────
// 查某板块的成分股（板块代码来自 get_sector_momentum / get_hot_concepts 返回的 code，形如 BK1626）。

type boardStocksArgs struct {
	BoardCode string `json:"board_code"`
	Limit     int    `json:"limit"`
}

func (s *Service) toolGetBoardStocks() (tool.InvokableTool, error) {
	info := &schema.ToolInfo{
		Name: "get_board_stocks",
		Desc: "查某板块的成分股（按涨跌幅降序）。board_code 用 get_sector_momentum/get_hot_concepts 返回的 code（形如 BK1626）。回答'XX板块有哪些股票/板块龙头'时用。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"board_code": strParam("板块代码，形如 BK1626", true),
			"limit":      intParam("返回前几名，默认15", false),
		}),
	}
	return defTool[*boardStocksArgs, string](info, func(ctx context.Context, a *boardStocksArgs) (string, error) {
		if s.sector == nil {
			return "未配置板块数据源", nil
		}
		code := strings.TrimSpace(a.BoardCode)
		if code == "" {
			return "请提供 board_code", nil
		}
		limit := a.Limit
		if limit <= 0 {
			limit = 15
		}
		stocks, err := s.sector.BoardStocks(ctx, code, limit)
		if err != nil {
			return "", err
		}
		if len(stocks) == 0 {
			return "未获取到该板块的成分股", nil
		}
		type rec struct {
			Code      string  `json:"code"`
			Name      string  `json:"name"`
			ChangePct float64 `json:"change_pct"`
		}
		out := make([]rec, 0, len(stocks))
		for _, st := range stocks {
			out = append(out, rec{Code: st.Code, Name: st.Name, ChangePct: st.ChangePct})
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	})
}

// ─────────────────────────── get_market_sentiment ───────────────────────────
// 市场情绪：涨跌家数/涨跌停/涨跌比。

func (s *Service) toolGetMarketSentiment() (tool.InvokableTool, error) {
	info := &schema.ToolInfo{
		Name: "get_market_sentiment",
		Desc: "查全市场涨跌家数、涨停/跌停家数、涨跌比，用于研判市场整体情绪（普涨/普跌/分化）。回答'市场情绪如何/赚钱效应/今天是普涨还是分化'时用。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}
	return defTool[*emptyArgs, string](info, func(ctx context.Context, _ *emptyArgs) (string, error) {
		if s.sector == nil {
			return "未配置市场宽度数据源", nil
		}
		mb, err := s.sector.MarketBreadth(ctx)
		if err != nil {
			return "", err
		}
		if mb == nil {
			return "未获取到市场宽度数据", nil
		}
		type rec struct {
			UpCount     int     `json:"up_count"`
			DownCount   int     `json:"down_count"`
			FlatCount   int     `json:"flat_count"`
			LimitUp     int     `json:"limit_up"`
			LimitDown   int     `json:"limit_down"`
			UpDownRatio float64 `json:"up_down_ratio"`
		}
		b, _ := json.Marshal(rec{
			UpCount: mb.UpCount, DownCount: mb.DownCount, FlatCount: mb.FlatCount,
			LimitUp: mb.LimitUp, LimitDown: mb.LimitDown, UpDownRatio: mb.UpDownRatio,
		})
		return string(b), nil
	})
}

// ─────────────────────────── get_global_indices ───────────────────────────
// 海外/亚太主要指数。

func (s *Service) toolGetGlobalIndices() (tool.InvokableTool, error) {
	info := &schema.ToolInfo{
		Name: "get_global_indices",
		Desc: "查海外/亚太主要指数实时涨跌（道指/纳指/标普500/恒生/日经225）。回答'美股昨晚怎么走/外围市场/全球市场'时用。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}
	return defTool[*emptyArgs, string](info, func(ctx context.Context, _ *emptyArgs) (string, error) {
		if s.sector == nil {
			return "未配置海外指数数据源", nil
		}
		fs, err := s.sector.ForeignIndices(ctx)
		if err != nil {
			return "", err
		}
		if len(fs) == 0 {
			return "未获取到海外指数数据", nil
		}
		type rec struct {
			Code      string  `json:"code"`
			Name      string  `json:"name"`
			Price     float64 `json:"price"`
			ChangePct float64 `json:"change_pct"`
		}
		out := make([]rec, 0, len(fs))
		for _, f := range fs {
			out = append(out, rec{Code: f.Code, Name: f.Name, Price: f.Price, ChangePct: f.ChangePct})
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	})
}
