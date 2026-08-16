package strategy

import (
	"fmt"
	"math"
	"testing"

	"github.com/jwcen/argent-go/internal/market"
)

// TestBacktest_NoLookAhead 是「前视偏差」的回归测试。
//
// 固定信号 signal=[1,0,1,0,1] 与收盘价 closes=[1,2,2,2,2]。
// 修正后的规则：第 i 日实际持仓 = signal[i-1]（信号次日执行）。
//   pos = [0, signal[0], signal[1], signal[2], signal[3]] = [0,1,0,1,0]
// day1 收益 r=(2/1-1)=1.0，pos[1]=1 → eqT 从 1 跳到 2。
//
// 若回归成「当日信号当日执行」（pos[i]=signal[i]），则 pos[1]=signal[1]=0，
// day1 持仓为 0，eqT[1]=1 —— 与预期 2 不符，测试会失败。
func TestBacktest_NoLookAhead(t *testing.T) {
	closes := []float64{1, 2, 2, 2, 2}
	signal := []int{1, 0, 1, 0, 1}

	res := Backtest(closes, signal, 0)

	wantTiming := []float64{1, 2, 2, 2, 2}
	if len(res.EquityTiming) != len(wantTiming) {
		t.Fatalf("curve len = %d, want %d", len(res.EquityTiming), len(wantTiming))
	}
	for i := range wantTiming {
		if math.Abs(res.EquityTiming[i]-wantTiming[i]) > 1e-9 {
			t.Fatalf("EquityTiming[%d] = %v, want %v  (pos[1] 应为 signal[0]=1)", i, res.EquityTiming[i], wantTiming[i])
		}
	}
	if math.Abs(res.TotalReturn-1.0) > 1e-9 {
		t.Fatalf("TotalReturn = %v, want 1.0", res.TotalReturn)
	}
	if math.Abs(res.HoldReturn-1.0) > 1e-9 {
		t.Fatalf("HoldReturn = %v, want 1.0", res.HoldReturn)
	}
}

// TestBacktest_CostDrains 验证每次调仓都会扣成本，且冲击为负。
func TestBacktest_CostDrains(t *testing.T) {
	// 反复穿越均线（每天切换），无成本时与持有同收益，有成本时应明显落后。
	closes := make([]float64, 200)
	for i := range closes {
		closes[i] = 100 * (1 + 0.01*math.Sin(float64(i)/3.0))
	}
	signal := make([]int, len(closes))
	for i := range signal {
		if i%2 == 0 {
			signal[i] = 1
		}
	}
	res := Backtest(closes, signal, 0.0010)
	// 频繁交易必被成本拖累：择时收益应 <= 持有收益
	if res.TotalReturn > res.HoldReturn+1e-6 {
		t.Fatalf("频繁交易不应跑赢持有：timing=%v hold=%v", res.TotalReturn, res.HoldReturn)
	}
	if res.Trades < 50 {
		t.Fatalf("应检测到大量交易段，got %d", res.Trades)
	}
}

// TestBacktest_BuyHoldBeatsFlat 长期净值为正的标的，一直持有应不亏（基准 sanity）。
func TestBacktest_BuyHoldSanity(t *testing.T) {
	closes := []float64{100, 110, 121, 133, 146, 161} // +10%/天
	signal := make([]int, len(closes))                // 永不入场
	res := Backtest(closes, signal, 0)
	if math.Abs(res.HoldReturn-(161.0/100.0-1)) > 1e-9 {
		t.Fatalf("HoldReturn = %v, want %v", res.HoldReturn, 161.0/100.0-1)
	}
	if res.TotalReturn != 0 { // 从未入场，择时收益 0
		t.Fatalf("never-in-market timing should be 0, got %v", res.TotalReturn)
	}
}

func TestRunBacktest_curvesBounded(t *testing.T) {
	k := make([]market.KlineDay, 300)
	for i := range k {
		// 震荡上行，足以生成指标与回测
		price := 100 * (1 + 0.003*float64(i) + 0.02*math.Sin(float64(i)/5.0))
		k[i] = market.KlineDay{
			Date:   fmt.Sprintf("2024%03d", i),
			Open:   price,
			Close:  price,
			High:   price * 1.01,
			Low:    price * 0.99,
			Volume: 1000,
		}
	}
	strats := []struct {
		name   string
		params BacktestParams
	}{
		{"single_ma", BacktestParams{Strategy: "single_ma", MAN: 60}},
		{"ma_cross", BacktestParams{Strategy: "ma_cross", MAFast: 20, MASlow: 60}},
		{"consensus", BacktestParams{Strategy: "consensus"}},
		{"bollinger", BacktestParams{Strategy: "bollinger", BollN: 20, BollK: 2.0}},
		{"rsi", BacktestParams{Strategy: "rsi", RSIPeriod: 14, RSIOversold: 30, RSIOverbought: 70}},
		{"macd", BacktestParams{Strategy: "macd"}},
		{"breakout", BacktestParams{Strategy: "breakout", BreakN: 60, BreakVol: 1.5}},
	}
	for _, s := range strats {
		rep, err := RunBacktest("600000", "测试银行", k, s.params)
		if err != nil {
			t.Fatalf("RunBacktest(%s) error: %v", s.name, err)
		}
		if len(rep.CurveTiming) == 0 || len(rep.CurveHold) == 0 {
			t.Fatalf("curves empty for %s", s.name)
		}
		if len(rep.CurveTiming) > 160 {
			t.Fatalf("curve not downsampled: %d", len(rep.CurveTiming))
		}
		if rep.CurveTiming[0] != 1 || rep.CurveHold[0] != 1 {
			t.Fatalf("curves should start at 1")
		}
		// 新增的指标：年化、夏普、交易明细都应该填出来
		if rep.Annualized == 0 && rep.TotalReturn != 0 {
			t.Errorf("%s: Annualized 未计算 (TotalReturn=%v)", s.name, rep.TotalReturn)
		}
		if len(rep.TradesDetail) != rep.Trades {
			t.Errorf("%s: TradesDetail 数量 %d 与 Trades %d 不一致", s.name, len(rep.TradesDetail), rep.Trades)
		}
	}
}

// TestBacktest_AnnualizedAndSharpe 验证年化/夏普在单调上行数据下的合理性。
func TestBacktest_AnnualizedAndSharpe(t *testing.T) {
	// 每天 +0.1% ± 微小噪声：有方向但有波动，Annualized 和 Sharpe 都应有意义
	closes := make([]float64, 252)
	for i := range closes {
		if i == 0 {
			closes[i] = 100
		} else {
			// 偶数日 +0.12%，奇数日 +0.08%（均值 +0.1%，有方差）
			delta := 0.0012
			if i%2 == 1 {
				delta = 0.0008
			}
			closes[i] = closes[i-1] * (1 + delta)
		}
	}
	signal := make([]int, len(closes))
	for i := range signal {
		signal[i] = 1 // 一直持仓
	}
	res := Backtest(closes, signal, 0)
	// 一直在场：TotalReturn ≈ (1.001)^251 - 1 ≈ 28.6%（精确值因 ± 噪声略低）
	want := math.Pow(1.001, 251) - 1
	if math.Abs(res.TotalReturn-want) > 5e-3 {
		t.Fatalf("TotalReturn = %v, want ≈%v", res.TotalReturn, want)
	}
	// 年化 ≈ (1+want)^(252/251)-1 ≈ 0.286
	wantAnn := math.Pow(1+want, 252.0/251.0) - 1
	if math.Abs(res.Annualized-wantAnn) > 1e-2 {
		t.Fatalf("Annualized = %v, want %v", res.Annualized, wantAnn)
	}
	if res.Annualized < 0.2 || res.Annualized > 0.4 {
		t.Fatalf("Annualized 应该在 0.2-0.4 之间，got %v", res.Annualized)
	}
	// 日收益有方差 → Sharpe 应远大于 0（高且为正）
	if res.Sharpe <= 0 {
		t.Fatalf("Sharpe 应该 > 0（有方向+波动），got %v", res.Sharpe)
	}
	if res.Sharpe < 5 {
		t.Fatalf("Sharpe 应该很高（强趋势+低波动），got %v", res.Sharpe)
	}
}

func TestRunBacktest_tooShort(t *testing.T) {
	k := make([]market.KlineDay, 10)
	for i := range k {
		k[i] = market.KlineDay{Date: "20240101", Close: 100}
	}
	if _, err := RunBacktest("600000", "x", k, BacktestParams{Strategy: "single_ma"}); err == nil {
		t.Fatalf("expected error for short kline")
	}
}
