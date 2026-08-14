// Package strategy 提供「诚实版」策略能力：
//   - 纯技术指标计算（indicators.go）：MA / EMA / MACD / RSI / KDJ
//   - 回测内核（backtest.go）：带前视偏差修正的均线择时 vs 买入持有
//   - 报告组装（report.go）：把指标 + 你自己的账本复盘，组合成中性参考
//
// 设计原则（源自一次真实回测的结论）：
//   简单均线择时在 A 股长期跑不赢「一直持有」，所以本包**不输出买卖建议**，
//   只输出「价格相对均线的位置」「趋势方向」这类中性事实，以及基于真实账本的
//   决策复盘（建仓至今盈亏）。任何「买入/卖出」措辞都被刻意降级为参考描述。
package strategy

// SMA 简单移动平均。返回与输入等长切片，warmup（不足 period）位置为 0。
func SMA(v []float64, n int) []float64 {
	out := make([]float64, len(v))
	if n <= 0 || len(v) < n {
		return out
	}
	var s float64
	for i := 0; i < len(v); i++ {
		s += v[i]
		if i >= n {
			s -= v[i-n]
		}
		if i >= n-1 {
			out[i] = s / float64(n)
		}
	}
	return out
}

// EMA 指数移动平均，种子取首值。返回与输入等长切片。
func EMA(v []float64, n int) []float64 {
	out := make([]float64, len(v))
	if len(v) == 0 || n <= 0 {
		return out
	}
	alpha := 2.0 / float64(n+1)
	out[0] = v[0]
	for i := 1; i < len(v); i++ {
		out[i] = alpha*v[i] + (1-alpha)*out[i-1]
	}
	return out
}

// MACD 返回 DIF / DEA / HIST 三条序列（标准 12/26/9，HIST=2*(DIF-DEA)）。
func MACD(closes []float64) (dif, dea, hist []float64) {
	e1 := EMA(closes, 12)
	e2 := EMA(closes, 26)
	n := len(closes)
	dif = make([]float64, n)
	for i := 0; i < n; i++ {
		dif[i] = e1[i] - e2[i]
	}
	dea = EMA(dif, 9)
	hist = make([]float64, n)
	for i := 0; i < n; i++ {
		hist[i] = 2 * (dif[i] - dea[i])
	}
	return
}

// RSI 相对强弱指标（Wilder 平滑，标准 14）。warmup 位置为 0。
func RSI(closes []float64, period int) []float64 {
	out := make([]float64, len(closes))
	if period <= 0 || len(closes) < period+1 {
		return out
	}
	var gainSum, lossSum float64
	for i := 1; i <= period; i++ {
		ch := closes[i] - closes[i-1]
		if ch >= 0 {
			gainSum += ch
		} else {
			lossSum -= ch
		}
	}
	avgGain := gainSum / float64(period)
	avgLoss := lossSum / float64(period)
	out[period] = rsiFrom(avgGain, avgLoss)
	for i := period + 1; i < len(closes); i++ {
		ch := closes[i] - closes[i-1]
		var g, l float64
		if ch >= 0 {
			g = ch
		} else {
			l = -ch
		}
		avgGain = (avgGain*float64(period-1) + g) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + l) / float64(period)
		out[i] = rsiFrom(avgGain, avgLoss)
	}
	return out
}

func rsiFrom(g, l float64) float64 {
	if l == 0 {
		if g == 0 {
			return 50
		}
		return 100
	}
	rs := g / l
	return 100 - 100/(1+rs)
}

// KDJ 随机指标（标准 9/3/3）。K、D 初值 50；返回 K/D/J 三条等长序列。
func KDJ(high, low, close []float64, n, m1, m2 int) (k, d, j []float64) {
	size := len(close)
	k = make([]float64, size)
	d = make([]float64, size)
	j = make([]float64, size)
	if size < n || n <= 0 {
		return
	}
	kPrev, dPrev := 50.0, 50.0
	for i := 0; i < size; i++ {
		if i >= n-1 {
			lo, hi := low[i], high[i]
			for t := i - n + 1; t <= i; t++ {
				if low[t] < lo {
					lo = low[t]
				}
				if high[t] > hi {
					hi = high[t]
				}
			}
			var rsv float64
			if hi > lo {
				rsv = (close[i] - lo) / (hi - lo) * 100
			} else {
				rsv = 50
			}
			kPrev = (float64(m1-1)*kPrev + rsv) / float64(m1)
			dPrev = (float64(m2-1)*dPrev + kPrev) / float64(m2)
		}
		k[i] = kPrev
		d[i] = dPrev
		j[i] = 3*kPrev - 2*dPrev
	}
	return
}

// lastValid 取序列最后一个非零值（用于快照当前指标）。
func lastValid(v []float64) float64 {
	for i := len(v) - 1; i >= 0; i-- {
		if v[i] != 0 {
			return v[i]
		}
	}
	return 0
}

// ── 择时信号（position builder）──
// 返回与收盘价等长的 0/1 序列：1=当日收盘信号倾向于「在场」。

// PosSingleMA 单均线：收盘价高于 N 日线则在场。
func PosSingleMA(closes []float64, n int) []int {
	ma := SMA(closes, n)
	pos := make([]int, len(closes))
	for i := range closes {
		if ma[i] > 0 && closes[i] > ma[i] {
			pos[i] = 1
		}
	}
	return pos
}

// PosCross 双均线金叉：快线高于慢线则在场。
func PosCross(closes []float64, fast, slow int) []int {
	mf, ms := SMA(closes, fast), SMA(closes, slow)
	pos := make([]int, len(closes))
	for i := range closes {
		if mf[i] > 0 && ms[i] > 0 && mf[i] > ms[i] {
			pos[i] = 1
		}
	}
	return pos
}

// PosConsensus 多指标共识：MA60 多头 + MACD 金叉 + KDJ 金叉，过半看多则在场。
// 各指标均带 warmup 保护，避免在数据不足时误发信号。
func PosConsensus(high, low, close []float64) []int {
	m60 := SMA(close, 60)
	dif, dea, _ := MACD(close)
	K, D, _ := KDJ(high, low, close, 9, 3, 3)
	pos := make([]int, len(close))
	for i := range close {
		v := 0
		if i >= 59 && m60[i] > 0 && close[i] > m60[i] {
			v++
		}
		if i >= 35 && dif[i] != 0 && dea[i] != 0 && dif[i] > dea[i] {
			v++
		}
		if i >= 9 && K[i] != 0 && D[i] != 0 && K[i] > D[i] {
			v++
		}
		if v >= 2 {
			pos[i] = 1
		}
	}
	return pos
}
