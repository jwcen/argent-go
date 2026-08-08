package portfolio

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jwcen/argent-go/internal/external"
)

// CurveMetrics 是净值曲线的汇总指标。
type CurveMetrics struct {
	ReturnPct      float64  `json:"return_pct"`       // 区间收益%（TWR 终点 − 100）
	MaxDrawdownPct float64  `json:"max_drawdown_pct"` // 最大回撤%（基于 TWR）
	BenchReturnPct *float64 `json:"bench_return_pct,omitempty"`
	ExcessPct      *float64 `json:"excess_pct,omitempty"`
	StartDate      string   `json:"start_date"`
	CurrentValue   float64  `json:"current_value"` // 当前组合账面价值
	Basis          string   `json:"basis"`         // "cost"=成本基线 / "market"=市值
}

// Curve 是组合净值曲线（对齐 Python portfolio_curve.build_curve）。
//
// 口径：无实时行情时按「成本基线」计（= 当前投入的账面价值），TWR 仍按
// r_t = (V_t − V_{t−1} − F_t)/(V_{t−1} + F_t) 严格剥离出入金，与基金净值同口径。
// 接入行情后只需把 V 换成市值即可升级为市值口径，TWR 公式不变。
type Curve struct {
	Dates     []string      `json:"dates"`
	Value     []float64     `json:"value"` // 组合账面价值（成本基线口径）
	TWR       []float64     `json:"twr"`   // 时间加权净值（起点 100）
	BenchName string        `json:"bench_name,omitempty"`
	Bench     []float64     `json:"bench,omitempty"` // 基准（起点 100），不可达时为空
	Metrics   CurveMetrics  `json:"metrics"`
	Note      string        `json:"note"`
}

// ExternalSource 是曲线计算所需的场外资产数据端口，避免 portfolio 直接耦合 external 内部结构。
// external.Repository 因方法签名一致可自动满足本接口。
type ExternalSource interface {
	ListAssets(ctx context.Context) ([]external.Asset, error)
	ListActions(ctx context.Context, assetID int64) ([]external.Action, error)
}

var (
	stockIn  = map[string]bool{"BUY": true, "ADD": true}
	stockOut = map[string]bool{"SELL": true}
	extIn    = map[string]bool{"BUY": true, "ADD": true, "DEPOSIT": true, "SUBSCRIBE": true}
	extOut   = map[string]bool{"REDEEM": true, "WITHDRAW": true, "SELL": true, "REDUCE": true}
	extIncome = map[string]bool{"INTEREST": true, "DIVIDEND": true}
)

// BuildCurve 计算组合净值曲线。days 为轴长上限（20~500），实际轴从最早交易日到今天。
// ext 为 nil 时只统计 A 股/ETF 持仓。
func (s *Service) BuildCurve(ctx context.Context, days int, ext ExternalSource) (*Curve, error) {
	if days <= 0 {
		days = 120
	}
	if days > 500 {
		days = 500
	}

	acts, err := s.repo.ListAllActions(ctx)
	if err != nil {
		return nil, err
	}

	var extActs []external.Action
	if ext != nil {
		assets, e := ext.ListAssets(ctx)
		if e == nil {
			for _, a := range assets {
				if la, e2 := ext.ListActions(ctx, a.ID); e2 == nil {
					extActs = append(extActs, la...)
				}
			}
		}
	}

	start, has := earliestTradeDate(acts, extActs)
	if !has {
		return &Curve{Note: "暂无持仓记录，无法绘制净值曲线"}, nil
	}
	end := s.now().Format("2006-01-02")
	axis := calendarDays(start, end)
	if len(axis) == 0 {
		return &Curve{Note: "暂无持仓记录，无法绘制净值曲线"}, nil
	}
	// days 是轴长上限（已 clamp 到 20~500）：从首笔交易到今天全量保留，
	// 超过上限时按步长抽稀（始终含首末点），不会把老持仓甩在窗口外。
	maxPts := days
	if maxPts < 20 {
		maxPts = 20
	}
	if maxPts > 500 {
		maxPts = 500
	}
	axis = downsample(axis, maxPts)
	n := len(axis)

	values := make([]float64, n)
	flows := make([]float64, n)
	idx := make(map[string]int, n)
	for i, d := range axis {
		idx[d] = i
	}

	// ---- A 股/ETF：逐日成本基线（running cost basis）----
	byCode := map[string][]Action{}
	for _, a := range acts {
		byCode[a.StockCode] = append(byCode[a.StockCode], a)
	}
	for _, list := range byCode {
		sort.Slice(list, func(i, j int) bool {
			if list[i].TradeDate != list[j].TradeDate {
				return list[i].TradeDate < list[j].TradeDate
			}
			return list[i].ID < list[j].ID
		})
		shares, cost := 0.0, 0.0
		ji := 0
		for i, d := range axis {
			for ji < len(list) && list[ji].TradeDate <= d {
				a := list[ji]
				switch a.ActionType {
				case "BUY", "ADD":
					sh := float64(a.Shares)
					amt := sh*a.Price + feeOf(a)
					shares += sh
					cost += amt
					if fi, ok := idx[d]; ok {
						flows[fi] += amt
					}
				case "SELL":
					sh := float64(a.Shares)
					amt := sh*a.Price - feeOf(a)
					if shares > 1e-9 {
						cost *= (shares - sh) / shares
					}
					shares -= sh
					if fi, ok := idx[d]; ok {
						flows[fi] -= amt
					}
				case "BONUS":
					shares += float64(a.Shares) // 0 成本，不改变 cost
				case "DIVIDEND":
					// 收益不进成本基线、不计入流量
				}
				ji++
			}
			if shares > 1e-9 {
				values[i] += cost
			} else {
				shares, cost = 0, 0
			}
		}
	}

	// ---- 场外：成本基线（累计净投入 + 利息分红），逐日前向累计 ----
	extDelta := make([]float64, n)
	for _, a := range extActs {
		if a.Status != "" && a.Status != "confirmed" {
			continue
		}
		fi, ok := idx[a.TradeDate]
		if !ok {
			continue
		}
		t := strings.ToUpper(a.ActionType)
		switch {
		case extIn[t] || extIncome[t]:
			extDelta[fi] += a.Amount
			if extIn[t] && fi < n {
				flows[fi] += a.Amount
			}
		case extOut[t]:
			extDelta[fi] -= a.Amount
			if fi < n {
				flows[fi] -= a.Amount
			}
		}
	}
	run := 0.0
	for i := 0; i < n; i++ {
		run += extDelta[i]
		if run < 0 {
			run = 0
		}
		values[i] += run
	}

	// ---- TWR（起点 100）----
	twr := make([]float64, n)
	nav := 100.0
	for i := 0; i < n; i++ {
		if i == 0 {
			twr[i] = nav
			continue
		}
		base := values[i-1] + flows[i]
		var r float64
		if base > 1e-6 {
			r = (values[i] - values[i-1] - flows[i]) / base
		}
		nav *= (1 + r)
		twr[i] = round4(nav)
	}

	mdd := maxDrawdown(twr)
	ret := 0.0
	if n > 0 {
		ret = twr[n-1] - 100
	}
	curve := &Curve{
		Dates:   axis,
		Value:   values,
		TWR:     twr,
		Metrics: CurveMetrics{
			ReturnPct:      round2(ret),
			MaxDrawdownPct: mdd,
			StartDate:      axis[0],
			CurrentValue:   round2(values[n-1]),
			Basis:          "cost",
		},
		Note: "TWR=时间加权收益（出入金已剥离，与基金净值同口径）。当前为成本基线口径（无实时行情时按投入成本计），" +
			"接入行情后自动升级为市值口径。纯客观展示，不构成任何买卖建议。",
	}
	return curve, nil
}

// ---- 纯函数助手 ----

func feeOf(a Action) float64 {
	if a.Fee != nil {
		return *a.Fee
	}
	return 0
}

func earliestTradeDate(acts []Action, ext []external.Action) (string, bool) {
	best := ""
	for _, a := range acts {
		if a.TradeDate == "" {
			continue
		}
		if best == "" || a.TradeDate < best {
			best = a.TradeDate
		}
	}
	for _, a := range ext {
		if a.TradeDate == "" {
			continue
		}
		if best == "" || a.TradeDate < best {
			best = a.TradeDate
		}
	}
	return best, best != ""
}

func addDays(dateStr string, n int) string {
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return dateStr
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

func calendarDays(start, end string) []string {
	s, err1 := time.Parse("2006-01-02", start)
	e, err2 := time.Parse("2006-01-02", end)
	if err1 != nil || err2 != nil || !e.After(s) {
		return nil
	}
	var out []string
	for t := s; !t.After(e); t = t.AddDate(0, 0, 1) {
		out = append(out, t.Format("2006-01-02"))
	}
	return out
}

// downsample 在轴过长时按步长抽稀，保证末点必含、长度 <= max。
func downsample(dates []string, max int) []string {
	if len(dates) <= max {
		return dates
	}
	stride := (len(dates) + max - 1) / max
	out := make([]string, 0, max)
	for i := 0; i < len(dates); i += stride {
		out = append(out, dates[i])
	}
	if out[len(out)-1] != dates[len(dates)-1] {
		out = append(out, dates[len(dates)-1])
	}
	return out
}

func maxDrawdown(series []float64) float64 {
	peak := math.Inf(-1)
	mdd := 0.0
	for _, v := range series {
		if v > peak {
			peak = v
		}
		if peak > 0 {
			if dd := v/peak - 1; dd < mdd {
				mdd = dd
			}
		}
	}
	return round2(mdd * 100)
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
