package strategy

import "math"

// Backtest 把「信号」与「收益」解耦，并**修正前视偏差**：
//
// signal[i] 表示「在第 i 日收盘时、依据截至当日的数据生成的仓位意愿」。
// 但现实中你只能用昨天的信号决定今天，所以：
//   第 i 日实际持仓 = signal[i-1]（信号次日才执行）
// 这正是初版回测算出 +19398% 荒谬收益的根因——当时直接用
// 「当日收盘 vs 当日均线」决定「当日仓位」，等于用当天的涨跌决定当天是否持仓。
//
// costRate 为单边交易成本（每次调仓买卖都扣），A 股按 0.10% 计（佣金+印花税+滑点）。
func Backtest(closes []float64, signal []int, costRate float64) *BacktestResult {
	n := len(closes)
	res := &BacktestResult{
		EquityTiming: make([]float64, n),
		EquityHold:   make([]float64, n),
	}
	if n < 2 {
		return res
	}
	// 次日执行：pos[i] = signal[i-1]，首日为 0（无信号）。
	pos := make([]int, n)
	for i := 1; i < n; i++ {
		pos[i] = signal[i-1]
	}

	eqT, eqH := 1.0, 1.0
	res.EquityTiming[0], res.EquityHold[0] = 1, 1
	prev := 0
	inMarket := 0

	for i := 1; i < n; i++ {
		r := closes[i]/closes[i-1] - 1
		eqT *= (1 + float64(pos[i])*r)
		eqH *= (1 + r)
		if pos[i] != prev {
			eqT *= (1 - costRate) // 调仓（买入或卖出）扣成本
		}
		prev = pos[i]
		inMarket += pos[i]
		res.EquityTiming[i] = eqT
		res.EquityHold[i] = eqH
	}

	res.TotalReturn = eqT - 1
	res.HoldReturn = eqH - 1
	res.Excess = res.TotalReturn - res.HoldReturn
	res.TimeInMarket = float64(inMarket) / float64(n)

	// 最大回撤（基于择时净值曲线）
	peak, maxDD := res.EquityTiming[0], 0.0
	for _, e := range res.EquityTiming {
		if e > peak {
			peak = e
		}
		dd := e/peak - 1
		if dd < maxDD {
			maxDD = dd
		}
	}
	res.MaxDD = maxDD

	// 交易明细：把持仓区间按连续 1 切分，每段算一次开仓→平仓收益。
	trades, wins := 0, 0
	i := 0
	for i < n {
		if pos[i] == 1 {
			start := i
			for i < n && pos[i] == 1 {
				i++
			}
			end := i - 1
			trades++
			openEq := 1.0
			if start > 0 {
				openEq = res.EquityTiming[start-1]
			}
			closeEq := res.EquityTiming[end]
			ret := closeEq/openEq - 1
			if ret > 0 {
				wins++
			}
			res.TradesDetail = append(res.TradesDetail, TradeDetail{
				OpenIdx:     start,
				CloseIdx:    end,
				OpenEquity:  openEq,
				CloseEquity: closeEq,
				Return:      ret,
			})
		} else {
			i++
		}
	}
	if trades > 0 {
		res.WinRate = float64(wins) / float64(trades)
	}
	res.Trades = trades

	// 年化收益（252 交易日/年）：(1+TotalReturn)^(252/(n-1)) - 1
	if 1+res.TotalReturn > 0 && n > 1 {
		res.Annualized = math.Pow(1+res.TotalReturn, 252.0/float64(n-1)) - 1
	} else {
		res.Annualized = res.TotalReturn // 极端行情兜底
	}

	// 夏普比率：日收益均值/样本标准差 × √252（无风险利率视为 0）
	dailyR := make([]float64, n-1)
	for j := 1; j < n; j++ {
		prevEq := res.EquityTiming[j-1]
		if prevEq > 0 {
			dailyR[j-1] = res.EquityTiming[j]/prevEq - 1
		}
	}
	if len(dailyR) > 1 {
		var sum float64
		for _, r := range dailyR {
			sum += r
		}
		mean := sum / float64(len(dailyR))
		var ss float64
		for _, r := range dailyR {
			d := r - mean
			ss += d * d
		}
		std := math.Sqrt(ss / float64(len(dailyR)-1))
		if std > 0 {
			res.Sharpe = mean / std * math.Sqrt(252)
		}
	}

	return res
}

// BacktestResult 回测输出。
type BacktestResult struct {
	EquityTiming []float64     `json:"equity_timing"` // 择时净值曲线（起点 1）
	EquityHold   []float64     `json:"equity_hold"`   // 买入持有净值曲线（起点 1）
	TotalReturn  float64       `json:"total_return"`  // 择时总收益（小数，0.88 = +88%）
	HoldReturn   float64       `json:"hold_return"`   // 买入持有总收益
	Excess       float64       `json:"excess"`        // 超额 = 择时 - 持有
	MaxDD        float64       `json:"max_dd"`        // 最大回撤（负数）
	WinRate      float64       `json:"win_rate"`      // 单笔交易胜率
	Trades       int           `json:"trades"`        // 交易段数
	TimeInMarket float64       `json:"time_in_market"`// 在场比例 0~1
	Annualized   float64       `json:"annualized"`    // 年化收益（按 252 交易日）
	Sharpe       float64       `json:"sharpe"`        // 夏普比率（rf=0）
	TradesDetail []TradeDetail `json:"trades_detail"` // 每笔交易明细
}

// TradeDetail 单笔交易明细：开仓日 / 平仓日 / 区间收益率。
type TradeDetail struct {
	OpenIdx     int     `json:"open_idx"`     // 开仓日索引（已修正前视偏差）
	CloseIdx    int     `json:"close_idx"`    // 平仓日索引
	OpenEquity  float64 `json:"open_equity"`  // 开仓时净值
	CloseEquity float64 `json:"close_equity"` // 平仓时净值
	Return      float64 `json:"return"`       // 该笔收益（小数）
}

// downsample 把曲线均匀抽稀到最多 maxPts 个点，便于前端 SVG 绘制。
func downsample(src []float64, maxPts int) []float64 {
	if len(src) <= maxPts || maxPts <= 0 {
		out := make([]float64, len(src))
		copy(out, src)
		return out
	}
	out := make([]float64, 0, maxPts)
	stride := float64(len(src)-1) / float64(maxPts-1)
	for i := 0; i < maxPts; i++ {
		idx := int(float64(i) * stride + 0.5)
		if idx >= len(src) {
			idx = len(src) - 1
		}
		out = append(out, src[idx])
	}
	return out
}
