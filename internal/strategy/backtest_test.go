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
	for _, strat := range []string{"single_ma", "ma_cross", "consensus"} {
		rep, err := RunBacktest("600000", "测试银行", k, BacktestParams{Strategy: strat, MAN: 60, MAFast: 20, MASlow: 60})
		if err != nil {
			t.Fatalf("RunBacktest(%s) error: %v", strat, err)
		}
		if len(rep.CurveTiming) == 0 || len(rep.CurveHold) == 0 {
			t.Fatalf("curves empty for %s", strat)
		}
		if len(rep.CurveTiming) > 160 {
			t.Fatalf("curve not downsampled: %d", len(rep.CurveTiming))
		}
		if rep.CurveTiming[0] != 1 || rep.CurveHold[0] != 1 {
			t.Fatalf("curves should start at 1")
		}
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
