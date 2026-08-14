package strategy

import (
	"math"
	"testing"
)

func TestSMA(t *testing.T) {
	got := SMA([]float64{1, 2, 3, 4, 5}, 3)
	want := []float64{0, 0, 2, 3, 4}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("SMA[3] index %d = %v, want %v", i, got[i], want[i])
		}
	}
	// warmup 不足返回全 0
	if got := SMA([]float64{1, 2}, 5); len(got) != 2 || got[0] != 0 || got[1] != 0 {
		t.Fatalf("SMA warmup wrong: %v", got)
	}
}

func TestEMA(t *testing.T) {
	v := []float64{2, 2, 2, 2, 2}
	got := EMA(v, 3)
	for i := range v {
		if math.Abs(got[i]-2) > 1e-9 {
			t.Fatalf("EMA flat expected 2, got %v", got[i])
		}
	}
}

func TestRSI_monotonic(t *testing.T) {
	// 单调上升 → 无亏损 → RSI = 100
	up := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	if r := RSI(up, 14); r[14] != 100 {
		t.Fatalf("RSI monotonic up want 100, got %v", r[14])
	}
	// 单调下降 → 无盈利 → RSI = 0
	down := []float64{15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
	if r := RSI(down, 14); r[14] != 0 {
		t.Fatalf("RSI monotonic down want 0, got %v", r[14])
	}
}

func TestMACD_flat(t *testing.T) {
	flat := make([]float64, 60)
	for i := range flat {
		flat[i] = 10
	}
	dif, dea, hist := MACD(flat)
	for i := range flat {
		if math.Abs(dif[i]) > 1e-9 || math.Abs(dea[i]) > 1e-9 || math.Abs(hist[i]) > 1e-9 {
			t.Fatalf("MACD of flat series should be 0 at %d: dif=%v dea=%v hist=%v", i, dif[i], dea[i], hist[i])
		}
	}
}

func TestKDJ_range(t *testing.T) {
	c := []float64{10, 11, 9, 12, 8, 13, 7, 14, 6, 15, 9, 12, 8, 11, 10, 13, 9, 12, 11, 10}
	h := []float64{11, 12, 10, 13, 9, 14, 8, 15, 7, 16, 10, 13, 9, 12, 11, 14, 10, 13, 12, 11}
	l := []float64{9, 10, 8, 11, 7, 12, 6, 13, 5, 14, 8, 11, 7, 10, 9, 12, 8, 11, 10, 9}
	K, D, J := KDJ(h, l, c, 9, 3, 3)
	for i := range c {
		if K[i] < 0 || K[i] > 100 || D[i] < 0 || D[i] > 100 {
			t.Fatalf("KDJ out of [0,100] at %d: K=%v D=%v", i, K[i], D[i])
		}
		if math.IsNaN(K[i]) || math.IsNaN(D[i]) || math.IsNaN(J[i]) {
			t.Fatalf("KDJ NaN at %d", i)
		}
	}
}

func TestPosSingleMA(t *testing.T) {
	// 价格先低于 MA 再高于 MA
	c := []float64{1, 1, 1, 1, 2, 3, 4}
	pos := PosSingleMA(c, 3)
	// 前 3 根都在 1，价=MA → 不在场；之后上涨 → 在场
	if pos[2] != 0 {
		t.Fatalf("warmup should be 0, got %d", pos[2])
	}
	if pos[6] != 1 {
		t.Fatalf("uptrend should be in market, got %d", pos[6])
	}
}
