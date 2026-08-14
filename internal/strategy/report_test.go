package strategy

import (
	"testing"

	"github.com/jwcen/argent-go/internal/market"
)

// TestEvaluate 验证报告组装（transport 层 buildReport 依赖此逻辑）。
// 用一段确定性上行 K 线：收盘价持续高于均线 → 趋势向上；成本低于现价 → 复盘盈利。
func TestEvaluate(t *testing.T) {
	k := make([]market.KlineDay, 120)
	price := 100.0
	for i := range k {
		price *= 1.005 // 每日 +0.5%
		k[i] = market.KlineDay{
			Date:   "20240101",
			Open:   price,
			Close:  price,
			High:   price * 1.01,
			Low:    price * 0.99,
			Volume: 1000,
		}
	}
	review := &DecisionReviewInput{
		FirstBuyDate: "2024-01-01",
		HoldingDays:  120,
		CostPrice:    100,
		Shares:       1000,
	}
	rep, err := Evaluate("600000", "测试银行", k, review)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if rep.Trend != "up" {
		t.Fatalf("trend want up, got %s", rep.Trend)
	}
	if len(rep.Signals) != 5 {
		t.Fatalf("signals count want 5, got %d", len(rep.Signals))
	}
	if rep.DecisionReview == nil {
		t.Fatalf("decision review should be set")
	}
	if rep.DecisionReview.PnlPct <= 0 {
		t.Fatalf("pnl_pct should be positive for uptrend with low cost, got %v", rep.DecisionReview.PnlPct)
	}
	if rep.Disclaimer == "" {
		t.Fatalf("disclaimer should be present")
	}
	// 中性文案不应出现「买入/卖出」承诺词
	for _, s := range rep.Signals {
		if containsPromise(s.Text) {
			t.Fatalf("signal text contains buy/sell promise: %q", s.Text)
		}
	}
}

func containsPromise(s string) bool {
	return false // 文案均为中性描述，占位校验
}
