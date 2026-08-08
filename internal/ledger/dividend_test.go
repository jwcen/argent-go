package ledger

import (
	"testing"

	"github.com/jwcen/argent-go/internal/domain"
)

func evt(y, m, d int, cash float64, bonus float64) DividendEvent {
	return DividendEvent{ExDate: mustDate(y, m, d), CashPerShare: domain.Yuan(cash), BonusRatio: bonus}
}

// ---- BONUS 送股：被动摊薄 ----

func TestBonus_DilutesCostPassively(t *testing.T) {
	// 10 元买 1000 股（成本 10000 元），随后 10 送 3 → 入账 300 股、价格 0
	s := ComputePositionState([]Action{
		act(ActionBuy, 10.0, 1000, mustDate(2025, 1, 10)),
		act(ActionBonus, 0, 300, mustDate(2025, 6, 20)),
	}, mustDate(2026, 1, 10))

	if s.Shares != 1300 {
		t.Fatalf("shares = %d, want 1300", s.Shares)
	}
	// 净投入 10000 元不变，分母变 1300 → 7.6923
	approx(t, s.CostPrice.YuanF(), 7.69, 0.01)
	approx(t, s.FIFOCostPrice.YuanF(), 7.69, 0.01)
	// 送股不产生任何已实现
	if s.RealizedPnL != 0 {
		t.Fatalf("realized_pnl = %s, want 0", s.RealizedPnL)
	}
}

func TestBonus_ThenSellUsesFIFOAcrossZeroPriceLot(t *testing.T) {
	// 送股批次价格为 0，卖出时应能吃到满额实现
	s := ComputePositionState([]Action{
		act(ActionBuy, 10.0, 1000, mustDate(2025, 1, 10)),
		act(ActionBonus, 0, 300, mustDate(2025, 6, 20)),
		act(ActionSell, 12.0, 1000, mustDate(2025, 9, 1)), // 全部吃掉 10 元那批
	}, mustDate(2026, 1, 10))

	if s.Shares != 300 {
		t.Fatalf("shares = %d, want 300", s.Shares)
	}
	// (12-10)*1000 = 2000
	approx(t, s.RealizedPnL.YuanF(), 2000, 0.01)
	// 剩下 300 股全是 0 成本的送股批次
	approx(t, s.FIFOCostPrice.YuanF(), 0, 0.01)
}

// ---- DIVIDEND 现金分红：进收入，不动股数 ----

func TestDividend_AddsIncomeNotShares(t *testing.T) {
	s := ComputePositionState([]Action{
		act(ActionBuy, 10.0, 1000, mustDate(2025, 1, 10)),
		act(ActionDividend, 0.35, 1000, mustDate(2025, 7, 5)), // 每股派 0.35
	}, mustDate(2026, 1, 10))

	if s.Shares != 1000 {
		t.Fatalf("shares = %d, want 1000 (分红不该改变股数)", s.Shares)
	}
	approx(t, s.IncomeRealized.YuanF(), 350, 0.01)
	// 分红同时进 realized_pnl 与 realized_carry
	approx(t, s.RealizedPnL.YuanF(), 350, 0.01)
	approx(t, s.RealizedCarry.YuanF(), 350, 0.01)
	// 但不摊薄成本（摊薄是 DiluteState 的职责，走除权事件那条路径）
	approx(t, s.CostPrice.YuanF(), 10.0, 0.01)
}

func TestDividend_CarryIncludesIncomeWithOpenEpisode(t *testing.T) {
	// 当前段有未平仓卖出实现 + 分红：
	// realized_pnl 含两者，realized_carry 只含分红（段内实现已摊进浮动成本）
	s := ComputePositionState([]Action{
		act(ActionBuy, 10.0, 1000, mustDate(2025, 1, 10)),
		act(ActionSell, 12.0, 400, mustDate(2025, 5, 10)),   // 实现 800
		act(ActionDividend, 0.5, 600, mustDate(2025, 7, 10)), // 分红 300
	}, mustDate(2026, 1, 10))

	approx(t, s.RealizedPnL.YuanF(), 1100, 0.01)   // 800 + 300
	approx(t, s.RealizedCarry.YuanF(), 300, 0.01)  // 只有分红
	approx(t, s.IncomeRealized.YuanF(), 300, 0.01)
}

func TestDividend_NegativeIgnored(t *testing.T) {
	s := ComputePositionState([]Action{
		act(ActionBuy, 10.0, 1000, mustDate(2025, 1, 10)),
		act(ActionDividend, -1.0, 1000, mustDate(2025, 7, 5)),
	}, mustDate(2026, 1, 10))

	if s.IncomeRealized != 0 {
		t.Fatalf("income = %s, want 0（负派息应被忽略）", s.IncomeRealized)
	}
}

// ---- 累计每股派息：边界条件 ----

func TestCumulativeCashDiv_Boundaries(t *testing.T) {
	events := []DividendEvent{
		evt(2024, 6, 20, 0.30, 0), // 开仓前 → 不算
		evt(2025, 6, 20, 0.40, 0), // 开仓后、今天前 → 算
		evt(2026, 6, 20, 0.50, 0), // 未来 → 不算
	}
	since := mustDate(2025, 1, 10)
	today := mustDate(2026, 1, 10)

	got := CumulativeCashDivPerShare(events, since, today)
	approx(t, got.YuanF(), 0.40, 0.0001)
}

func TestCumulativeCashDiv_ExDateEqualsOpenDateExcluded(t *testing.T) {
	// ex_date == 开仓日：买到的已经是除权价，吃不到这次分红
	events := []DividendEvent{evt(2025, 1, 10, 0.40, 0)}
	got := CumulativeCashDivPerShare(events, mustDate(2025, 1, 10), mustDate(2026, 1, 10))
	if got != 0 {
		t.Fatalf("got %s, want 0（ex_date == since 必须排除）", got)
	}
}

func TestCumulativeCashDiv_ExDateEqualsTodayIncluded(t *testing.T) {
	events := []DividendEvent{evt(2026, 1, 10, 0.40, 0)}
	got := CumulativeCashDivPerShare(events, mustDate(2025, 1, 10), mustDate(2026, 1, 10))
	approx(t, got.YuanF(), 0.40, 0.0001)
}

// ---- 摊薄：幂等 + 截断 ----

func TestDiluteState_Idempotent(t *testing.T) {
	s := ComputePositionState([]Action{
		act(ActionBuy, 10.0, 1000, mustDate(2025, 1, 10)),
	}, mustDate(2026, 1, 10))

	div := domain.Yuan(0.40)
	DiluteState(&s, div)
	first := s.CostPrice
	DiluteState(&s, div) // 再来一次
	DiluteState(&s, div) // 再再来一次

	if s.CostPrice != first {
		t.Fatalf("重复摊薄不幂等: %s != %s", s.CostPrice, first)
	}
	approx(t, s.CostPrice.YuanF(), 9.60, 0.001)
	approx(t, s.CostPriceRaw.YuanF(), 10.00, 0.001) // 原始值保留
	approx(t, s.DividendPerShare.YuanF(), 0.40, 0.001)
}

func TestDiluteState_ClampsAtZero(t *testing.T) {
	s := ComputePositionState([]Action{
		act(ActionBuy, 0.30, 1000, mustDate(2025, 1, 10)),
	}, mustDate(2026, 1, 10))

	DiluteState(&s, domain.Yuan(0.50)) // 派息比成本还高
	if s.CostPrice != 0 {
		t.Fatalf("cost = %s, want 0（负成本必须截断）", s.CostPrice)
	}
}

// ---- 开仓日 ----

func TestOpenedAt_UsesEarliestRemainingLot(t *testing.T) {
	s := ComputePositionState([]Action{
		act(ActionBuy, 10.0, 500, mustDate(2025, 3, 1)),
		act(ActionBuy, 11.0, 500, mustDate(2025, 8, 1)),
		act(ActionSell, 12.0, 500, mustDate(2025, 9, 1)), // 吃掉 3/1 那批
	}, mustDate(2026, 1, 10))

	want := mustDate(2025, 8, 1)
	if !s.OpenedAt.Equal(want) {
		t.Fatalf("opened_at = %s, want %s（清掉的批次不该再算开仓日）", s.OpenedAt, want)
	}
}

func TestOpenedAt_ResetsAfterFullClear(t *testing.T) {
	s := ComputePositionState([]Action{
		act(ActionBuy, 10.0, 500, mustDate(2025, 3, 1)),
		act(ActionSell, 12.0, 500, mustDate(2025, 5, 1)), // 清仓
		act(ActionBuy, 9.0, 300, mustDate(2025, 11, 1)),  // 新段
	}, mustDate(2026, 1, 10))

	want := mustDate(2025, 11, 1)
	if !s.OpenedAt.Equal(want) {
		t.Fatalf("opened_at = %s, want %s", s.OpenedAt, want)
	}
	// 新段成本不该被上一段盈亏污染
	approx(t, s.CostPrice.YuanF(), 9.0, 0.01)
}

// ---- 除权还原 ----

func TestRestoreFromQFQ_CashOnly(t *testing.T) {
	// 前复权价 9.60，其后有一次每股派 0.40 → 还原成 10.00
	events := []DividendEvent{evt(2025, 6, 20, 0.40, 0)}
	got := RestoreFromQFQ(domain.Yuan(9.60), events, mustDate(2025, 1, 10), mustDate(2026, 1, 10))
	approx(t, got.YuanF(), 10.00, 0.01)
}

func TestRestoreFromQFQ_CashAndBonus(t *testing.T) {
	// 正向: qfq = (raw - cash) / (1 + bonus)
	// raw=10, cash=0.4, bonus=0.5 → qfq = 9.6/1.5 = 6.40
	events := []DividendEvent{evt(2025, 6, 20, 0.40, 0.5)}
	got := RestoreFromQFQ(domain.Yuan(6.40), events, mustDate(2025, 1, 10), mustDate(2026, 1, 10))
	approx(t, got.YuanF(), 10.00, 0.01)
}

func TestRestoreFromQFQ_MultiEventOrderMatters(t *testing.T) {
	// 两次事件，必须由近及远套逆变换。
	// 正向 raw=20 →(2025-03: cash0.5,bonus0)→ 19.5 →(2025-09: cash0,bonus1.0)→ 9.75
	events := []DividendEvent{
		evt(2025, 3, 1, 0.50, 0),
		evt(2025, 9, 1, 0, 1.0),
	}
	got := RestoreFromQFQ(domain.Yuan(9.75), events, mustDate(2025, 1, 1), mustDate(2026, 1, 10))
	approx(t, got.YuanF(), 20.00, 0.02)
}

func TestRestoreFromQFQ_NoEventsIsIdentity(t *testing.T) {
	got := RestoreFromQFQ(domain.Yuan(12.34), nil, mustDate(2025, 1, 1), mustDate(2026, 1, 10))
	if got != domain.Yuan(12.34) {
		t.Fatalf("got %s, want 12.34", got)
	}
}
