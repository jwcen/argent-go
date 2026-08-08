package portfolio

import (
	"context"
	"testing"
	"time"

	"github.com/jwcen/argent-go/internal/external"
)

type fakeExt struct {
	assets  []external.Asset
	actions []external.Action
}

func (f *fakeExt) ListAssets(ctx context.Context) ([]external.Asset, error) { return f.assets, nil }
func (f *fakeExt) ListActions(ctx context.Context, assetID int64) ([]external.Action, error) {
	var out []external.Action
	for _, a := range f.actions {
		if a.AssetID == assetID {
			out = append(out, a)
		}
	}
	return out, nil
}

func floatp(v float64) *float64 { return &v }

func newCurveSvc(actions []*Action, ext *fakeExt, now time.Time) *Service {
	r := newFakeRepo()
	for _, a := range actions {
		r.actions = append(r.actions, a)
	}
	svc := NewService(r)
	svc.SetClock(func() time.Time { return now })
	return svc
}

func approx(a, b, eps float64) bool { return (a-b) < eps && (b-a) < eps }

func TestCurve_ProfitableSellMovesTWR(t *testing.T) {
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	acts := []*Action{
		{ID: 1, StockCode: "600519", ActionType: ActionBuy, Price: 10, Shares: 1000, TradeDate: "2025-01-10"},
		{ID: 2, StockCode: "600519", ActionType: ActionSell, Price: 15, Shares: 500, TradeDate: "2025-03-01"},
	}
	svc := newCurveSvc(acts, nil, now)
	// days=200 → 窗口起点早于首笔交易，且无抽稀（轴长<500）
	c, err := svc.BuildCurve(context.Background(), 200, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Dates) == 0 {
		t.Fatal("empty axis")
	}
	if !approx(c.TWR[len(c.TWR)-1], 200, 0.5) {
		t.Errorf("TWR 终点应为 ~200（卖出实现 +100%%），实际 %v", c.TWR[len(c.TWR)-1])
	}
	if !approx(c.Metrics.ReturnPct, 100, 0.5) {
		t.Errorf("区间收益应为 ~100，实际 %v", c.Metrics.ReturnPct)
	}
	// 卖出后账面价值=剩余成本基线 5000
	last := c.Value[len(c.Value)-1]
	if !approx(last, 5000, 1) {
		t.Errorf("末日账面价值应为 5000，实际 %v", last)
	}
}

func TestCurve_BonusKeepsCostBasis(t *testing.T) {
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	acts := []*Action{
		{ID: 1, StockCode: "600519", ActionType: ActionBuy, Price: 10, Shares: 1000, TradeDate: "2025-01-10"},
		{ID: 2, StockCode: "600519", ActionType: ActionBonus, Price: 0, Shares: 300, TradeDate: "2025-02-01"},
	}
	svc := newCurveSvc(acts, nil, now)
	c, err := svc.BuildCurve(context.Background(), 200, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 送转不改变成本基线：末日价值仍为 10000，TWR 全程 100
	if !approx(c.Value[len(c.Value)-1], 10000, 1) {
		t.Errorf("送转后账面价值应仍为 10000，实际 %v", c.Value[len(c.Value)-1])
	}
	for _, v := range c.TWR {
		if !approx(v, 100, 1e-6) {
			t.Errorf("送转场景 TWR 应恒为 100，实际 %v", v)
		}
	}
}

func TestCurve_DividendNoMove(t *testing.T) {
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	acts := []*Action{
		{ID: 1, StockCode: "600519", ActionType: ActionBuy, Price: 10, Shares: 1000, TradeDate: "2025-01-10"},
		{ID: 2, StockCode: "600519", ActionType: ActionDividend, Price: 0.4, Shares: 1000, TradeDate: "2025-02-01"},
	}
	svc := newCurveSvc(acts, nil, now)
	c, err := svc.BuildCurve(context.Background(), 200, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !approx(c.Value[len(c.Value)-1], 10000, 1) {
		t.Errorf("分红后账面价值应仍为 10000，实际 %v", c.Value[len(c.Value)-1])
	}
	if !approx(c.TWR[len(c.TWR)-1], 100, 1e-6) {
		t.Errorf("分红场景 TWR 应恒为 100，实际 %v", c.TWR[len(c.TWR)-1])
	}
}

func TestCurve_LossCreatesDrawdown(t *testing.T) {
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	acts := []*Action{
		{ID: 1, StockCode: "600519", ActionType: ActionBuy, Price: 10, Shares: 1000, TradeDate: "2025-01-10"},
		{ID: 2, StockCode: "600519", ActionType: ActionSell, Price: 5, Shares: 500, TradeDate: "2025-03-01"},
	}
	svc := newCurveSvc(acts, nil, now)
	c, err := svc.BuildCurve(context.Background(), 200, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 半仓亏损卖出：TWR 跌到 ~66.7，最大回撤 ~ -33.3
	if !approx(c.TWR[len(c.TWR)-1], 66.67, 1) {
		t.Errorf("TWR 终点应为 ~66.67，实际 %v", c.TWR[len(c.TWR)-1])
	}
	if c.Metrics.MaxDrawdownPct > -30 || c.Metrics.MaxDrawdownPct < -36 {
		t.Errorf("最大回撤应约 -33.3，实际 %v", c.Metrics.MaxDrawdownPct)
	}
}

func TestCurve_ExternalAssetsCounted(t *testing.T) {
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	acts := []*Action{
		{ID: 1, StockCode: "600519", ActionType: ActionBuy, Price: 10, Shares: 1000, TradeDate: "2025-01-10"},
	}
	ext := &fakeExt{
		assets:  []external.Asset{{ID: 1, AssetType: external.AssetFund, Code: "110011"}},
		actions: []external.Action{{ID: 1, AssetID: 1, ActionType: "DEPOSIT", Amount: 20000, TradeDate: "2025-02-01", Status: "confirmed"}},
	}
	svc := newCurveSvc(acts, ext, now)
	c, err := svc.BuildCurve(context.Background(), 200, ext)
	if err != nil {
		t.Fatal(err)
	}
	// 买入 10000 + 场外存入 20000 = 30000
	if !approx(c.Value[len(c.Value)-1], 30000, 1) {
		t.Errorf("含场外资产末日价值应为 30000，实际 %v", c.Value[len(c.Value)-1])
	}
}

func TestCurve_Empty(t *testing.T) {
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	svc := newCurveSvc(nil, nil, now)
	c, err := svc.BuildCurve(context.Background(), 120, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Note == "" {
		t.Error("空持仓应返回提示，而非空曲线")
	}
	if len(c.Dates) != 0 {
		t.Error("空持仓不应有轴")
	}
}

func TestCurve_DaysClamp(t *testing.T) {
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	acts := []*Action{
		{ID: 1, StockCode: "600519", ActionType: ActionBuy, Price: 10, Shares: 1000, TradeDate: "2025-01-10"},
	}
	svc := newCurveSvc(acts, nil, now)
	c, _ := svc.BuildCurve(context.Background(), 9999, nil) // 超过 500 上限
	if len(c.Dates) > 500 {
		t.Errorf("轴长不应超过 500，实际 %d", len(c.Dates))
	}
}
