package ledger

import (
	"math"
	"testing"
	"time"

	"github.com/jwcen/argent-go/internal/domain"
)

// ---- 对拍辅助 ----

func mustDate(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}

func act(t ActionType, price float64, shares int, date time.Time) Action {
	return Action{Type: t, Price: domain.Yuan(price), Shares: int64(shares), TradeDate: date}
}

func approx(t *testing.T, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("approx: got %.6f, want %.6f (±%.6f)", got, want, tol)
	}
}

func between(t *testing.T, got, lo, hi float64) {
	t.Helper()
	if got < lo || got > hi {
		t.Fatalf("between: got %.6f, not in [%.6f, %.6f]", got, lo, hi)
	}
}

// ============================================================
// FIFO 持仓账本 —— 对照 tests/test_position_ledger.py
// ============================================================

func TestFifo_SingleBuy(t *testing.T) {
	s := ComputePositionState([]Action{
		act(ActionBuy, 10.0, 100, mustDate(2025, 10, 21)),
	}, mustDate(2026, 4, 22))

	if s.Shares != 100 {
		t.Fatalf("shares = %d, want 100", s.Shares)
	}
	if s.CostPrice != domain.Yuan(10.0) {
		t.Fatalf("cost_price = %s, want 10.00", s.CostPrice)
	}
	if s.WeightedDays < 180 || s.WeightedDays > 185 {
		t.Fatalf("weighted_days = %d, want in [180,185]", s.WeightedDays)
	}
}

func TestFifo_TwoBuysWeightedDays(t *testing.T) {
	s := ComputePositionState([]Action{
		act(ActionBuy, 10.0, 100, mustDate(2025, 4, 22)),
		act(ActionBuy, 10.0, 100, mustDate(2026, 4, 21)),
	}, mustDate(2026, 4, 22))

	if s.Shares != 200 {
		t.Fatalf("shares = %d, want 200", s.Shares)
	}
	if s.CostPrice != domain.Yuan(10.0) {
		t.Fatalf("cost_price = %s, want 10.00", s.CostPrice)
	}
	if s.WeightedDays < 180 || s.WeightedDays > 185 {
		t.Fatalf("weighted_days = %d, want in [180,185]", s.WeightedDays)
	}
}

func TestFifo_WeightedAvgCost(t *testing.T) {
	s := ComputePositionState([]Action{
		act(ActionBuy, 12.0, 100, mustDate(2025, 1, 1)),
		act(ActionBuy, 8.0, 200, mustDate(2026, 1, 1)),
	}, mustDate(2026, 4, 22))

	if s.Shares != 300 {
		t.Fatalf("shares = %d, want 300", s.Shares)
	}
	// 综合成本 = (12*100 + 8*200)/300 = 9.333
	between(t, s.CostPrice.YuanF(), 9.32, 9.34)
}

func TestFifo_SellConsumesOldestFirst(t *testing.T) {
	s := ComputePositionState([]Action{
		act(ActionBuy, 12.0, 100, mustDate(2025, 1, 1)),
		act(ActionBuy, 8.0, 100, mustDate(2026, 1, 1)),
		act(ActionSell, 11.0, 50, mustDate(2026, 4, 1)),
	}, mustDate(2026, 4, 22))

	if s.Shares != 150 {
		t.Fatalf("shares = %d, want 150", s.Shares)
	}
	// 综合成本法: (100*12 + 100*8 - 50*11)/150 = 9.667
	between(t, s.CostPrice.YuanF(), 9.65, 9.68)
	// FIFO 成本法: (50*12 + 100*8)/150 = 9.333
	between(t, s.FIFOCostPrice.YuanF(), 9.32, 9.34)
	if len(s.Lots) != 2 {
		t.Fatalf("lots = %d, want 2", len(s.Lots))
	}
}

func TestFifo_CompleteSelloffReturnsZero(t *testing.T) {
	s := ComputePositionState([]Action{
		act(ActionBuy, 10.0, 100, mustDate(2025, 1, 1)),
		act(ActionSell, 12.0, 100, mustDate(2026, 1, 1)),
	}, mustDate(2026, 4, 22))

	if s.Shares != 0 {
		t.Fatalf("shares = %d, want 0", s.Shares)
	}
	if !s.CostPrice.IsZero() {
		t.Fatalf("cost_price = %s, want 0", s.CostPrice)
	}
	if len(s.Lots) != 0 {
		t.Fatalf("lots = %d, want 0", len(s.Lots))
	}
}

func TestFifo_AddTreatedAsAcquisition(t *testing.T) {
	s := ComputePositionState([]Action{
		act(ActionBuy, 10.0, 100, mustDate(2025, 1, 1)),
		act(ActionAdd, 9.0, 100, mustDate(2025, 6, 1)),
		act(ActionAdd, 8.0, 100, mustDate(2026, 1, 1)),
	}, mustDate(2026, 4, 22))

	if s.Shares != 300 {
		t.Fatalf("shares = %d, want 300", s.Shares)
	}
	if s.CostPrice != domain.Yuan(9.0) {
		t.Fatalf("cost_price = %s, want 9.00", s.CostPrice)
	}
}

func TestFifo_ClearThenRebuyResetsCostBasis(t *testing.T) {
	s := ComputePositionState([]Action{
		act(ActionBuy, 10.0, 100, mustDate(2025, 1, 1)),
		act(ActionSell, 12.0, 100, mustDate(2025, 6, 1)),
		act(ActionBuy, 8.0, 100, mustDate(2026, 1, 1)),
	}, mustDate(2026, 4, 22))

	if s.Shares != 100 {
		t.Fatalf("shares = %d, want 100", s.Shares)
	}
	// 重新建仓后成本就是 8.0，不被上一轮 +200 盈利摊低（原版 bug 会算成 6.0）
	if s.CostPrice != domain.Yuan(8.0) {
		t.Fatalf("cost_price = %s, want 8.00", s.CostPrice)
	}
	if s.RealizedPnL != domain.Yuan(200.0) {
		t.Fatalf("realized_pnl = %s, want 200.00", s.RealizedPnL)
	}
	if s.RealizedCarry != domain.Yuan(200.0) {
		t.Fatalf("realized_carry = %s, want 200.00", s.RealizedCarry)
	}
}

func TestFifo_ClearThenRebuyAtLossCarry(t *testing.T) {
	s := ComputePositionState([]Action{
		act(ActionBuy, 9.41, 100, mustDate(2026, 4, 29)),
		act(ActionSell, 9.11, 100, mustDate(2026, 5, 7)),
		act(ActionBuy, 7.88, 100, mustDate(2026, 5, 29)),
	}, mustDate(2026, 6, 2))

	if s.Shares != 100 {
		t.Fatalf("shares = %d, want 100", s.Shares)
	}
	if s.CostPrice != domain.Yuan(7.88) {
		t.Fatalf("cost_price = %s, want 7.88", s.CostPrice)
	}
	if s.RealizedPnL != domain.Yuan(-30.0) {
		t.Fatalf("realized_pnl = %s, want -30.00", s.RealizedPnL)
	}
	if s.RealizedCarry != domain.Yuan(-30.0) {
		t.Fatalf("realized_carry = %s, want -30.00", s.RealizedCarry)
	}
}

func TestFifo_PartialSellWithinEpisodeStillFolds(t *testing.T) {
	s := ComputePositionState([]Action{
		act(ActionBuy, 12.0, 100, mustDate(2025, 1, 1)),
		act(ActionBuy, 8.0, 100, mustDate(2026, 1, 1)),
		act(ActionSell, 11.0, 50, mustDate(2026, 4, 1)),
	}, mustDate(2026, 4, 22))

	if s.Shares != 150 {
		t.Fatalf("shares = %d, want 150", s.Shares)
	}
	// 一段内部分卖出仍走综合成本法摊薄，不重置
	between(t, s.CostPrice.YuanF(), 9.65, 9.68)
	between(t, s.FIFOCostPrice.YuanF(), 9.32, 9.34)
	// 没清过仓 → carry=0，当前浮动已含这部分
	if !s.RealizedCarry.IsZero() {
		t.Fatalf("realized_carry = %s, want 0", s.RealizedCarry)
	}
}

func TestFifo_OutOfOrderInputSortedByTradeDate(t *testing.T) {
	s := ComputePositionState([]Action{
		act(ActionSell, 12.0, 50, mustDate(2026, 4, 1)),
		act(ActionBuy, 10.0, 100, mustDate(2025, 1, 1)),
	}, mustDate(2026, 4, 22))

	// 应先生效 BUY 再 SELL：剩余 50@10，综合成本 (100*10-50*12)/50 = 8.0
	if s.Shares != 50 {
		t.Fatalf("shares = %d, want 50", s.Shares)
	}
	if s.CostPrice != domain.Yuan(8.0) {
		t.Fatalf("cost_price = %s, want 8.00", s.CostPrice)
	}
	if s.FIFOCostPrice != domain.Yuan(10.0) {
		t.Fatalf("fifo_cost_price = %s, want 10.00", s.FIFOCostPrice)
	}
}

// ============================================================
// 经济学 —— 对照 tests/test_economics.py
// ============================================================

func TestEcon_RealCostZeroDays(t *testing.T) {
	approx(t, RealCost(100.0, 0, 0.03), 100.0, 1e-9)
}

func TestEcon_RealCostOneYear3pct(t *testing.T) {
	approx(t, RealCost(100.0, 365, 0.03), 103.0, 0.01)
}

func TestEcon_RealCostCompounds(t *testing.T) {
	approx(t, RealCost(100.0, 730, 0.03), 106.09, 0.01)
}

func TestEcon_RealCostCustomRate(t *testing.T) {
	approx(t, RealCost(100.0, 365, 0.05), 105.0, 0.01)
}

func TestEcon_OpportunityCost(t *testing.T) {
	approx(t, OpportunityCost(10000.0, 365, 0.03), 300.0, 0.5)
}

func TestEcon_DailyOpportunityCost(t *testing.T) {
	approx(t, DailyOpportunityCost(10000.0, 0.03), 0.822, 0.01)
}

func TestEcon_RequiredExitPrice(t *testing.T) {
	approx(t, RequiredExitPrice(10.0, 2.0, 0.03), 10.609, 0.01)
}

func TestEcon_HoldVsCutCutBetter(t *testing.T) {
	r := HoldVsCutNPV(10000.0, 11000.0, 0.5, 2.0, 0.06)
	if r.Recommendation != "cut" {
		t.Fatalf("recommendation = %q, want cut", r.Recommendation)
	}
	if !(r.CutFV > r.HoldFV) {
		t.Fatalf("cut_fv (%v) should be > hold_fv (%v)", r.CutFV, r.HoldFV)
	}
}

func TestEcon_HoldVsCutHoldBetter(t *testing.T) {
	r := HoldVsCutNPV(10000.0, 20000.0, 0.9, 2.0, 0.06)
	if r.Recommendation != "hold" {
		t.Fatalf("recommendation = %q, want hold", r.Recommendation)
	}
}

func TestEcon_HoldVsCutNeutral(t *testing.T) {
	r := HoldVsCutNPV(10000.0, 12000.0, 1.0, 2.0, 0.06)
	if r.Recommendation != "hold" {
		t.Fatalf("recommendation = %q, want hold", r.Recommendation)
	}
}

// ============================================================
// 解套规划 —— 对照 tests/test_unwind_planner.py
// ============================================================

func TestUnwind_ComputePriorityDeepLossHigher(t *testing.T) {
	pA := ComputePriority(0.30, 0.5, 0.03, 0.0)
	pB := ComputePriority(0.10, 0.5, 0.03, 0.0)
	if !(pA > pB) {
		t.Fatalf("deep-loss priority (%v) should exceed shallow (%v)", pA, pB)
	}
}

func TestUnwind_ComputePriorityDowntrendPenalty(t *testing.T) {
	pNormal := ComputePriority(0.2, 0.5, 0.03, 0.0)
	pDown := ComputePriority(0.2, 0.5, 0.03, 0.5)
	if !(pDown < pNormal) {
		t.Fatalf("downtrend priority (%v) should be < normal (%v)", pDown, pNormal)
	}
}

func TestUnwind_AllocateBudgetsProportional(t *testing.T) {
	res := AllocateBudgets([]StockPriority{
		{StockCode: "A", Priority: 1.0},
		{StockCode: "B", Priority: 3.0},
	}, 4000.0)
	if res["A"] != 1000.0 {
		t.Fatalf("A = %v, want 1000", res["A"])
	}
	if res["B"] != 3000.0 {
		t.Fatalf("B = %v, want 3000", res["B"])
	}
}

func TestUnwind_AllocateBudgetsZeroPriorities(t *testing.T) {
	res := AllocateBudgets([]StockPriority{
		{StockCode: "A", Priority: 0.0},
	}, 1000.0)
	if res["A"] != 1000.0 {
		t.Fatalf("A = %v, want 1000", res["A"])
	}
}

func TestUnwind_GenerateTranchesBasic(t *testing.T) {
	tranches := GenerateTranches(10.0, 0.5, []float64{9.2, 8.5}, 8.8, 7.5, 3000.0)
	if len(tranches) < 3 {
		t.Fatalf("tranches = %d, want >= 3", len(tranches))
	}
	for _, tr := range tranches {
		if tr.TriggerPrice >= 10.0 {
			t.Fatalf("trigger_price %v should be < 10.0", tr.TriggerPrice)
		}
		if tr.Shares <= 0 {
			t.Fatalf("shares %d should be > 0", tr.Shares)
		}
	}
}

func TestUnwind_GenerateTranchesPyramid(t *testing.T) {
	tranches := GenerateTranches(10.0, 0.5, []float64{9.2, 8.5}, 8.8, 7.5, 5000.0)
	shares := make([]int64, len(tranches))
	for i, tr := range tranches {
		shares[i] = tr.Shares
	}
	for i := 1; i < len(shares); i++ {
		if shares[i] < shares[i-1] {
			t.Fatalf("shares not non-decreasing at %d: %v", i, shares)
		}
	}
}

func TestUnwind_GenerateTranchesHealth(t *testing.T) {
	tranches := GenerateTranches(10.0, 0.5, []float64{9.2, 8.5}, 8.8, 7.5, 5000.0)
	if tranches[0].RequiresHealth != "any" {
		t.Fatalf("first health = %q, want any", tranches[0].RequiresHealth)
	}
	last := tranches[len(tranches)-1].RequiresHealth
	if last != "yellow" && last != "green" {
		t.Fatalf("last health = %q, want yellow or green", last)
	}
}

func TestUnwind_CheckFeasibilityFeasible(t *testing.T) {
	r := CheckTrancheFeasibility(300, 12.74, 10.0, 200, 13.0, 2.0, 0.03)
	// new_cost = (3822 + 2000)/500 = 11.644; required = 11.644*1.03^2 = 12.353 < 13.0
	if !r.Feasible {
		t.Fatalf("feasible = %v, want true", r.Feasible)
	}
}

func TestUnwind_CheckFeasibilityNotFeasible(t *testing.T) {
	r := CheckTrancheFeasibility(300, 20.0, 15.0, 100, 18.0, 2.0, 0.03)
	// new_cost = 18.75; required = 18.75*1.03^2 = 19.89 > 18.0
	if r.Feasible {
		t.Fatalf("feasible = %v, want false", r.Feasible)
	}
	if !(r.RequiredPrice > 18.0) {
		t.Fatalf("required_price %v should exceed historical high 18.0", r.RequiredPrice)
	}
}
