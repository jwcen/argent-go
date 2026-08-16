package strategy

import (
	"fmt"

	"github.com/jwcen/argent-go/internal/market"
)

// ── 报告领域结构（snake_case，供 JSON 序列化）──

// IndicatorSnapshot 当前指标快照。
type IndicatorSnapshot struct {
	MA20  float64 `json:"ma20"`
	MA60  float64 `json:"ma60"`
	MA120 float64 `json:"ma120"`
	MA250 float64 `json:"ma250"`
	MACD  struct {
		DIF  float64 `json:"dif"`
		DEA  float64 `json:"dea"`
		Hist float64 `json:"hist"`
	} `json:"macd"`
	RSI float64 `json:"rsi"`
	KDJ struct {
		K float64 `json:"k"`
		D float64 `json:"d"`
		J float64 `json:"j"`
	} `json:"kdj"`
}

// SignalItem 一条中性信号描述（刻意不含「买入/卖出」承诺）。
type SignalItem struct {
	Name  string `json:"name"`
	State string `json:"state"` // above/below/golden/dead/overbought/oversold/neutral
	Text  string `json:"text"`
}

// DecisionReviewInput 由 transport 层从用户账本填充（不含持仓价格，用 K 线末值替代）。
type DecisionReviewInput struct {
	FirstBuyDate string  `json:"first_buy_date"`
	HoldingDays  int     `json:"holding_days"`
	CostPrice    float64 `json:"cost_price"`
	Shares       int64   `json:"shares"`
}

// DecisionReview 决策复盘：基于真实账本算出的「建仓至今」事实。
type DecisionReview struct {
	FirstBuyDate string  `json:"first_buy_date"`
	HoldingDays  int     `json:"holding_days"`
	CostPrice    float64 `json:"cost_price"`
	LastClose    float64 `json:"last_close"`
	PnlPct       float64 `json:"pnl_pct"`
	PnlAbs       float64 `json:"pnl_abs"`
	Shares       int64   `json:"shares"`
}

// Report 单只持仓的诚实策略报告。
type Report struct {
	Code           string           `json:"code"`
	Name           string           `json:"name"`
	LastClose      float64          `json:"last_close"`
	Indicators     IndicatorSnapshot `json:"indicators"`
	Signals        []SignalItem     `json:"signals"`
	Trend          string           `json:"trend"` // up/down/sideways
	DecisionReview *DecisionReview  `json:"decision_review,omitempty"`
	Disclaimer     string           `json:"disclaimer"`
}

// BacktestParams 回测请求参数。
type BacktestParams struct {
	Strategy string `json:"strategy"` // single_ma | ma_cross | consensus | bollinger | rsi | macd | breakout | grid
	Period   int    `json:"period"`   // 0=日K(默认), 102=周K, 103=月K（仅数据源支持时生效）
	MAN      int    `json:"ma_n"`
	MAFast   int    `json:"ma_fast"`
	MASlow   int    `json:"ma_slow"`
	// 布林均值回归
	BollN int     `json:"boll_n"`
	BollK float64 `json:"boll_k"`
	// RSI 择时
	RSIPeriod    int `json:"rsi_period"`
	RSIOversold  int `json:"rsi_oversold"`
	RSIOverbought int `json:"rsi_overbought"`
	// 放量突破
	BreakN   int     `json:"break_n"`
	BreakVol float64 `json:"break_vol"`
	// 滚动网格
	GridN      int `json:"grid_n"`
	GridLevels int `json:"grid_levels"`
}

// BacktestReport 回测结果（含净值曲线，已抽稀）。
type BacktestReport struct {
	Code         string         `json:"code"`
	Name         string         `json:"name"`
	Strategy     string         `json:"strategy"`
	TotalReturn  float64        `json:"total_return"`
	HoldReturn   float64        `json:"hold_return"`
	Excess       float64        `json:"excess"`
	MaxDD        float64        `json:"max_dd"`
	WinRate      float64        `json:"win_rate"`
	Trades       int            `json:"trades"`
	TimeInMarket float64        `json:"time_in_market"`
	Annualized   float64        `json:"annualized"`     // 年化收益
	Sharpe       float64        `json:"sharpe"`         // 夏普比率
	CurveTiming  []float64      `json:"curve_timing"`
	CurveHold    []float64      `json:"curve_hold"`
	TradesDetail []TradeDetail  `json:"trades_detail"`  // 交易明细
	Note         string         `json:"note"`
}

const costPerSwitch = 0.0010 // 单边 0.10%（佣金+印花税+滑点）

func closesOf(k []market.KlineDay) []float64 {
	out := make([]float64, len(k))
	for i, x := range k {
		out[i] = x.Close
	}
	return out
}
func highsOf(k []market.KlineDay) []float64 {
	out := make([]float64, len(k))
	for i, x := range k {
		out[i] = x.High
	}
	return out
}
func lowsOf(k []market.KlineDay) []float64 {
	out := make([]float64, len(k))
	for i, x := range k {
		out[i] = x.Low
	}
	return out
}
func volumesOf(k []market.KlineDay) []float64 {
	out := make([]float64, len(k))
	for i, x := range k {
		out[i] = x.Volume
	}
	return out
}

// intToFloat 把 0/1 型信号转成 0/1 分数仓位，统一喂给 Backtest 的 []float64 入参。
func intToFloat(sig []int) []float64 {
	out := make([]float64, len(sig))
	for i, v := range sig {
		out[i] = float64(v)
	}
	return out
}

// Evaluate 组装单只持仓的诚实策略报告。纯函数，不依赖外部服务。
func Evaluate(code, name string, klines []market.KlineDay, review *DecisionReviewInput) (*Report, error) {
	if len(klines) < 2 {
		return nil, fmt.Errorf("k线数据不足")
	}
	c := closesOf(klines)
	hi := highsOf(klines)
	lo := lowsOf(klines)

	rep := &Report{
		Code:      code,
		Name:      name,
		LastClose: c[len(c)-1],
		Disclaimer: "技术指标为中性参考，决策复盘基于你的真实账本；均不构成投资建议。",
	}

	rep.Indicators.MA20 = lastValid(SMA(c, 20))
	rep.Indicators.MA60 = lastValid(SMA(c, 60))
	rep.Indicators.MA120 = lastValid(SMA(c, 120))
	rep.Indicators.MA250 = lastValid(SMA(c, 250))

	dif, dea, hist := MACD(c)
	rep.Indicators.MACD.DIF = lastValid(dif)
	rep.Indicators.MACD.DEA = lastValid(dea)
	rep.Indicators.MACD.Hist = lastValid(hist)
	rep.Indicators.RSI = lastValid(RSI(c, 14))

	K, D, J := KDJ(hi, lo, c, 9, 3, 3)
	rep.Indicators.KDJ.K = lastValid(K)
	rep.Indicators.KDJ.D = lastValid(D)
	rep.Indicators.KDJ.J = lastValid(J)

	last := rep.LastClose
	rep.Signals = []SignalItem{
		priceVsMA("价格 vs 20 日均线", last, rep.Indicators.MA20),
		priceVsMA("价格 vs 60 日均线", last, rep.Indicators.MA60),
		macdSignal(rep.Indicators.MACD.DIF, rep.Indicators.MACD.DEA),
		rsiSignal(rep.Indicators.RSI),
		kdjSignal(rep.Indicators.KDJ.K, rep.Indicators.KDJ.D),
	}

	// 趋势：短均线在长均线之上且价在短线之上 → 向上；反之向下。
	if rep.Indicators.MA20 > 0 && rep.Indicators.MA60 > 0 {
		switch {
		case last > rep.Indicators.MA20 && rep.Indicators.MA20 > rep.Indicators.MA60:
			rep.Trend = "up"
		case last < rep.Indicators.MA20 && rep.Indicators.MA20 < rep.Indicators.MA60:
			rep.Trend = "down"
		default:
			rep.Trend = "sideways"
		}
	}

	if review != nil {
		lastClose := rep.LastClose
		pnlPct, pnlAbs := 0.0, 0.0
		if review.CostPrice > 0 {
			pnlPct = (lastClose - review.CostPrice) / review.CostPrice
			pnlAbs = (lastClose - review.CostPrice) * float64(review.Shares)
		}
		rep.DecisionReview = &DecisionReview{
			FirstBuyDate: review.FirstBuyDate,
			HoldingDays:  review.HoldingDays,
			CostPrice:    review.CostPrice,
			LastClose:    lastClose,
			PnlPct:       pnlPct,
			PnlAbs:       pnlAbs,
			Shares:       review.Shares,
		}
	}

	return rep, nil
}

// RunBacktest 用指定策略对历史 K 线做回测。纯函数。
func RunBacktest(code, name string, klines []market.KlineDay, p BacktestParams) (*BacktestReport, error) {
	if len(klines) < 60 {
		return nil, fmt.Errorf("k线数据不足（至少需 60 根），无法回测")
	}
	c := closesOf(klines)
	hi := highsOf(klines)
	lo := lowsOf(klines)

	var signal []float64
	switch p.Strategy {
	case "ma_cross":
		fast, slow := p.MAFast, p.MASlow
		if fast <= 0 {
			fast = 20
		}
		if slow <= 0 {
			slow = 60
		}
		signal = intToFloat(PosCross(c, fast, slow))
	case "consensus":
		signal = intToFloat(PosConsensus(hi, lo, c))
	case "bollinger":
		signal = intToFloat(PosBollingerMeanReversion(c, p.BollN, p.BollK))
	case "rsi":
		signal = intToFloat(PosRSI(c, p.RSIPeriod, p.RSIOversold, p.RSIOverbought))
	case "macd":
		signal = intToFloat(PosMACD(c))
	case "breakout":
		signal = intToFloat(PosBreakout(c, volumesOf(klines), p.BreakN, p.BreakVol))
	case "grid":
		signal = PosGrid(hi, lo, c, p.GridN, p.GridLevels)
	default: // single_ma
		n := p.MAN
		if n <= 0 {
			n = 60
		}
		signal = intToFloat(PosSingleMA(c, n))
	}

	res := Backtest(c, signal, costPerSwitch)

	strategyName := map[string]string{
		"single_ma": "单均线择时",
		"ma_cross":  "双均线金叉",
		"consensus": "多指标共识",
		"bollinger": "布林均值回归",
		"rsi":       "RSI 择时",
		"macd":      "MACD 金叉死叉",
		"breakout":  "放量突破",
		"grid":      "滚动网格",
	}[p.Strategy]
	if strategyName == "" {
		strategyName = "单均线择时"
	}

	return &BacktestReport{
		Code:         code,
		Name:         name,
		Strategy:     strategyName,
		TotalReturn:  res.TotalReturn,
		HoldReturn:   res.HoldReturn,
		Excess:       res.Excess,
		MaxDD:        res.MaxDD,
		WinRate:      res.WinRate,
		Trades:       res.Trades,
		TimeInMarket: res.TimeInMarket,
		Annualized:   res.Annualized,
		Sharpe:       res.Sharpe,
		CurveTiming:  downsample(res.EquityTiming, 160),
		CurveHold:    downsample(res.EquityHold, 160),
		TradesDetail: res.TradesDetail,
		Note:         fmt.Sprintf("单边交易成本假设 %.2f%%（含佣金、印花税、滑点）；信号次日执行，已修正前视偏差。", costPerSwitch*100),
	}, nil
}

// SplitReport 分段回测：样本内（train，前半段）vs 样本外（test，后半段）。
// 用于暴露「策略是否过拟合历史」：样本内很强、样本外崩掉 = 过拟合嫌疑。
type SplitReport struct {
	Code     string          `json:"code"`
	Name     string          `json:"name"`
	Strategy string          `json:"strategy"`
	Train    *BacktestReport `json:"train"` // 样本内
	Test     *BacktestReport `json:"test"`  // 样本外
	Note     string          `json:"note"`
}

// RunBacktestSplit 把历史 K 线对半切，分别跑同一策略，做样本内外对比。
func RunBacktestSplit(code, name string, klines []market.KlineDay, p BacktestParams) (*SplitReport, error) {
	if len(klines) < 120 {
		return nil, fmt.Errorf("k线数据不足（至少需 120 根），无法分段回测")
	}
	half := len(klines) / 2
	train, err := RunBacktest(code, name, klines[:half], p)
	if err != nil {
		return nil, err
	}
	test, err := RunBacktest(code, name, klines[half:], p)
	if err != nil {
		return nil, err
	}
	return &SplitReport{
		Code:     code,
		Name:     name,
		Strategy: train.Strategy,
		Train:    train,
		Test:     test,
		Note:     "前半段为样本内、后半段为样本外。若样本内超额很高而样本外转负，说明策略可能过拟合历史。",
	}, nil
}

// ── 中性信号文案（刻意避免「买入/卖出」承诺）──

func priceVsMA(name string, price, ma float64) SignalItem {
	if ma <= 0 {
		return SignalItem{Name: name, State: "neutral", Text: "均线尚未成型"}
	}
	if price > ma {
		return SignalItem{Name: name, State: "above", Text: fmt.Sprintf("价格在均线上方（+%.1f%%）", (price/ma-1)*100)}
	}
	return SignalItem{Name: name, State: "below", Text: fmt.Sprintf("价格在均线下方（%.1f%%）", (price/ma-1)*100)}
}

func macdSignal(dif, dea float64) SignalItem {
	if dif == 0 || dea == 0 {
		return SignalItem{Name: "MACD", State: "neutral", Text: "信号未成型"}
	}
	if dif > dea {
		return SignalItem{Name: "MACD", State: "golden", Text: "DIF 在 DEA 上方（动能偏多）"}
	}
	return SignalItem{Name: "MACD", State: "dead", Text: "DIF 在 DEA 下方（动能偏空）"}
}

func rsiSignal(rsi float64) SignalItem {
	if rsi == 0 {
		return SignalItem{Name: "RSI(14)", State: "neutral", Text: "信号未成型"}
	}
	switch {
	case rsi >= 70:
		return SignalItem{Name: "RSI(14)", State: "overbought", Text: fmt.Sprintf("%.0f · 进入超买区", rsi)}
	case rsi <= 30:
		return SignalItem{Name: "RSI(14)", State: "oversold", Text: fmt.Sprintf("%.0f · 进入超卖区", rsi)}
	default:
		return SignalItem{Name: "RSI(14)", State: "neutral", Text: fmt.Sprintf("%.0f · 中性区间", rsi)}
	}
}

func kdjSignal(k, d float64) SignalItem {
	if k == 0 || d == 0 {
		return SignalItem{Name: "KDJ(9,3,3)", State: "neutral", Text: "信号未成型"}
	}
	switch {
	case k >= 80:
		return SignalItem{Name: "KDJ(9,3,3)", State: "overbought", Text: fmt.Sprintf("K=%.0f · 超买区", k)}
	case k <= 20:
		return SignalItem{Name: "KDJ(9,3,3)", State: "oversold", Text: fmt.Sprintf("K=%.0f · 超卖区", k)}
	default:
		if k > d {
			return SignalItem{Name: "KDJ(9,3,3)", State: "golden", Text: fmt.Sprintf("K=%.0f>D=%.0f · 偏多", k, d)}
		}
		return SignalItem{Name: "KDJ(9,3,3)", State: "dead", Text: fmt.Sprintf("K=%.0f<D=%.0f · 偏空", k, d)}
	}
}
