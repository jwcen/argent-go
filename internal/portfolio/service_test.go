package portfolio

import (
	"context"
	"testing"
	"time"
)

// fakeRepo 是内存版 Repository，用于单测。
// 跟 Stage 2 的 auth fakeRepo 同一思路：手写而非生成式 mock。
type fakeRepo struct {
	holdings map[string]*Holding
	actions  []*Action
	brokers  []*Broker
	thesis   map[string]*Thesis
	watch    map[string]*WatchlistItem
	divs     []*DividendEvent
	nextID   int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		holdings: make(map[string]*Holding),
		thesis:   make(map[string]*Thesis),
		watch:    make(map[string]*WatchlistItem),
		nextID:   1,
	}
}

func (f *fakeRepo) ListHoldings(ctx context.Context) ([]Holding, error) {
	out := make([]Holding, 0, len(f.holdings))
	for _, h := range f.holdings {
		out = append(out, *h)
	}
	return out, nil
}

func (f *fakeRepo) GetHolding(ctx context.Context, code string) (*Holding, error) {
	if h, ok := f.holdings[code]; ok {
		return h, nil
	}
	return nil, ErrNotFound
}

func (f *fakeRepo) UpsertHolding(ctx context.Context, h *Holding) error {
	f.holdings[h.StockCode] = h
	return nil
}

func (f *fakeRepo) DeleteHolding(ctx context.Context, code string) error {
	delete(f.holdings, code)
	return nil
}

func (f *fakeRepo) ListActions(ctx context.Context, code string) ([]Action, error) {
	var out []Action
	for _, a := range f.actions {
		if a.StockCode == code {
			out = append(out, *a)
		}
	}
	return out, nil
}

func (f *fakeRepo) ListAllActions(ctx context.Context) ([]Action, error) {
	out := make([]Action, len(f.actions))
	for i, a := range f.actions {
		out[i] = *a
	}
	return out, nil
}

func (f *fakeRepo) GetAction(ctx context.Context, id int64) (*Action, error) {
	for _, a := range f.actions {
		if a.ID == id {
			cp := *a
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeRepo) CreateAction(ctx context.Context, a *Action) (int64, error) {
	a.ID = f.nextID
	f.nextID++
	f.actions = append(f.actions, a)
	return a.ID, nil
}

func (f *fakeRepo) UpdateAction(ctx context.Context, a *Action) error {
	for i, ex := range f.actions {
		if ex.ID == a.ID {
			f.actions[i] = a
			return nil
		}
	}
	return ErrNotFound
}

func (f *fakeRepo) DeleteAction(ctx context.Context, id int64) error {
	for i, a := range f.actions {
		if a.ID == id {
			f.actions = append(f.actions[:i], f.actions[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

func (f *fakeRepo) ListBrokers(ctx context.Context) ([]Broker, error) {
	out := make([]Broker, len(f.brokers))
	for i, b := range f.brokers {
		out[i] = *b
	}
	return out, nil
}

func (f *fakeRepo) GetDefaultBroker(ctx context.Context) (*Broker, error) {
	for _, b := range f.brokers {
		if b.IsDefault {
			return b, nil
		}
	}
	return nil, ErrNotFound
}

func (f *fakeRepo) CreateBroker(ctx context.Context, b *Broker) (int64, error) {
	b.ID = f.nextID
	f.nextID++
	f.brokers = append(f.brokers, b)
	return b.ID, nil
}

func (f *fakeRepo) UpdateBroker(ctx context.Context, b *Broker) error {
	for i, ex := range f.brokers {
		if ex.ID == b.ID {
			f.brokers[i] = b
			return nil
		}
	}
	return ErrNotFound
}

func (f *fakeRepo) DeleteBroker(ctx context.Context, id int64) error {
	for i, b := range f.brokers {
		if b.ID == id {
			f.brokers = append(f.brokers[:i], f.brokers[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

func (f *fakeRepo) GetThesis(ctx context.Context, code string) (*Thesis, error) {
	if t, ok := f.thesis[code]; ok {
		return t, nil
	}
	return nil, ErrNotFound
}

func (f *fakeRepo) UpsertThesis(ctx context.Context, t *Thesis) error {
	f.thesis[t.Code] = t
	return nil
}

func (f *fakeRepo) DeleteThesis(ctx context.Context, code string) error {
	delete(f.thesis, code)
	return nil
}

func (f *fakeRepo) ListWatchlist(ctx context.Context) ([]WatchlistItem, error) {
	out := make([]WatchlistItem, 0, len(f.watch))
	for _, w := range f.watch {
		out = append(out, *w)
	}
	return out, nil
}

func (f *fakeRepo) AddWatchlist(ctx context.Context, w *WatchlistItem) error {
	f.watch[w.StockCode] = w
	return nil
}

func (f *fakeRepo) RemoveWatchlist(ctx context.Context, code string) error {
	delete(f.watch, code)
	return nil
}

// ---- tests ----

func TestCreateAction_RecomputesHolding(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	fixedTime := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return fixedTime })

	// 买入 100 股 @10
	id, err := svc.CreateAction(context.Background(), &Action{
		StockCode:  "600519",
		ActionType: ActionBuy,
		Price:      10.0,
		Shares:     100,
		TradeDate:  "2026-01-10",
	})
	if err != nil {
		t.Fatalf("CreateAction: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	// 持仓应该自动重算
	h, err := repo.GetHolding(context.Background(), "600519")
	if err != nil {
		t.Fatalf("GetHolding: %v", err)
	}
	if h.Shares != 100 {
		t.Errorf("shares = %d, want 100", h.Shares)
	}
	if h.CostPrice != 10.0 {
		t.Errorf("cost_price = %v, want 10.0", h.CostPrice)
	}

	// 加仓 100 股 @20
	_, err = svc.CreateAction(context.Background(), &Action{
		StockCode:  "600519",
		ActionType: ActionBuy,
		Price:      20.0,
		Shares:     100,
		TradeDate:  "2026-01-12",
	})
	if err != nil {
		t.Fatalf("CreateAction 2: %v", err)
	}

	h, _ = repo.GetHolding(context.Background(), "600519")
	if h.Shares != 200 {
		t.Errorf("shares after add = %d, want 200", h.Shares)
	}
	// 综合成本 = (100*10 + 100*20) / 200 = 15
	if h.CostPrice != 15.0 {
		t.Errorf("cost_price after add = %v, want 15.0", h.CostPrice)
	}

	// 卖出 50 股 @25
	_, err = svc.CreateAction(context.Background(), &Action{
		StockCode:  "600519",
		ActionType: ActionSell,
		Price:      25.0,
		Shares:     50,
		TradeDate:  "2026-01-14",
	})
	if err != nil {
		t.Fatalf("CreateAction sell: %v", err)
	}

	h, _ = repo.GetHolding(context.Background(), "600519")
	if h.Shares != 150 {
		t.Errorf("shares after sell = %d, want 150", h.Shares)
	}
}

func TestDeleteAction_RecomputesAndCanClearHolding(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	svc.SetClock(func() time.Time { return time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC) })

	// 买入后删掉这笔流水 → 持仓应被删除
	id, _ := svc.CreateAction(context.Background(), &Action{
		StockCode:  "000001",
		ActionType: ActionBuy,
		Price:      15.0,
		Shares:     200,
		TradeDate:  "2026-01-10",
	})

	if err := svc.DeleteAction(context.Background(), id); err != nil {
		t.Fatalf("DeleteAction: %v", err)
	}

	if _, err := repo.GetHolding(context.Background(), "000001"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound after deleting all actions, got %v", err)
	}
}

func TestRealized(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	svc.SetClock(func() time.Time { return time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC) })

	// 买 100@10，卖 100@15 → realized = (15-10)*100 = 500
	svc.CreateAction(context.Background(), &Action{
		StockCode: "600519", ActionType: ActionBuy, Price: 10.0, Shares: 100, TradeDate: "2026-01-10",
	})
	svc.CreateAction(context.Background(), &Action{
		StockCode: "600519", ActionType: ActionSell, Price: 15.0, Shares: 100, TradeDate: "2026-01-12",
	})

	results, err := svc.Realized(context.Background())
	if err != nil {
		t.Fatalf("Realized: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.StockCode != "600519" {
		t.Errorf("code = %s", r.StockCode)
	}
	if r.RealizedPnL != 500.0 {
		t.Errorf("realized_pnl = %v, want 500.0", r.RealizedPnL)
	}
	if r.RealizedCarry != 500.0 {
		t.Errorf("realized_carry = %v, want 500.0 (清仓段)", r.RealizedCarry)
	}
}

func TestEstimateTradeFee(t *testing.T) {
	amount := 10.0 * 1000 // 10000
	baseReg := amount*transferRate + amount*(exchangeHandleRate+regulatoryFeeRate)
	minComm := defaultCommissionRate * amount
	if minComm < defaultCommissionMin {
		minComm = defaultCommissionMin
	}

	tests := []struct {
		name       string
		actionType ActionType
		price      float64
		shares     int64
		broker     *Broker
		want       float64
	}{
		// amount=10000, commission rate=0.01854% → 1.854 < min 5 → commission=5
		{"buy no broker", ActionBuy, 10.0, 1000, nil,
			minComm + baseReg},
		{"sell no broker", ActionSell, 10.0, 1000, nil,
			minComm + amount*stampRate + baseReg},
		{"add is free", ActionAdd, 10.0, 100, nil, 0},
		{"with broker rate 0.001", ActionBuy, 10.0, 1000, &Broker{StockRate: 0.001, StockMin: 1},
			amount*0.001 + baseReg}, // 10 > min 1, so commission = amount*rate
		{"min commission", ActionBuy, 1.0, 1, nil,
			defaultCommissionMin + 1*transferRate + 1*(exchangeHandleRate+regulatoryFeeRate)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTradeFee(tt.actionType, tt.price, tt.shares, tt.broker)
			if abs(got-tt.want) > 0.01 {
				t.Errorf("EstimateTradeFee = %.4f, want %.4f", got, tt.want)
			}
		})
	}
}

func TestValidateAction(t *testing.T) {
	tests := []struct {
		name    string
		action  *Action
		wantErr error
	}{
		{"valid buy", &Action{StockCode: "600519", ActionType: ActionBuy, Price: 10, Shares: 100}, nil},
		{"empty code", &Action{StockCode: "", ActionType: ActionBuy, Price: 10, Shares: 100}, ErrInvalidCode},
		// DIVIDEND / BONUS 自 Stage 11 起是合法类型，这里换一个真正不存在的
		{"bad type", &Action{StockCode: "600519", ActionType: "SPLIT", Price: 10, Shares: 100}, ErrInvalidAction},
		{"dividend now valid", &Action{StockCode: "600519", ActionType: ActionDividend, Price: 0.4, Shares: 100}, nil},
		{"bonus now valid", &Action{StockCode: "600519", ActionType: ActionBonus, Price: 0, Shares: 100}, nil},
		// 大小写必须严格匹配：action_type 是大写契约，小写会绕过 ledger 的 switch 变成静默空动作
		{"lowercase rejected", &Action{StockCode: "600519", ActionType: "buy", Price: 10, Shares: 100}, ErrInvalidAction},
		{"negative price", &Action{StockCode: "600519", ActionType: ActionBuy, Price: -1, Shares: 100}, ErrInvalidPrice},
		{"zero shares", &Action{StockCode: "600519", ActionType: ActionBuy, Price: 10, Shares: 0}, ErrInvalidShares},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAction(tt.action)
			if tt.wantErr == nil && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if tt.wantErr != nil && err != tt.wantErr {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// ---- Dividend events (fakeRepo) ----

func (f *fakeRepo) ListDividendEvents(ctx context.Context, code string) ([]DividendEvent, error) {
	out := make([]DividendEvent, 0)
	for _, e := range f.divs {
		if e.StockCode == code {
			out = append(out, *e)
		}
	}
	return out, nil
}

func (f *fakeRepo) ListAllDividendEvents(ctx context.Context) ([]DividendEvent, error) {
	out := make([]DividendEvent, 0, len(f.divs))
	for _, e := range f.divs {
		out = append(out, *e)
	}
	return out, nil
}

func (f *fakeRepo) UpsertDividendEvent(ctx context.Context, e *DividendEvent) (int64, error) {
	for _, ex := range f.divs {
		if ex.StockCode == e.StockCode && ex.ExDate == e.ExDate {
			ex.CashPerShare = e.CashPerShare
			ex.BonusRatio = e.BonusRatio
			ex.Source = e.Source
			return ex.ID, nil
		}
	}
	e.ID = f.nextID
	f.nextID++
	cp := *e
	f.divs = append(f.divs, &cp)
	return e.ID, nil
}

func (f *fakeRepo) DeleteDividendEvent(ctx context.Context, id int64) error {
	for i, e := range f.divs {
		if e.ID == id {
			f.divs = append(f.divs[:i], f.divs[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

// ============================================================
// 分红 / 除权（Stage 11）
// ============================================================

func fixedClock(y, m, d int) func() time.Time {
	return func() time.Time { return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC) }
}

func TestService_BonusActionDilutesHolding(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	svc.SetClock(fixedClock(2026, 1, 10))
	ctx := context.Background()

	if _, err := svc.CreateAction(ctx, &Action{
		StockCode: "600519", ActionType: ActionBuy, Price: 10, Shares: 1000, TradeDate: "2025-01-10",
	}); err != nil {
		t.Fatal(err)
	}
	// 送股：即便调用方把 price 传成非 0，也必须被强制归零
	if _, err := svc.CreateAction(ctx, &Action{
		StockCode: "600519", ActionType: ActionBonus, Price: 9.99, Shares: 300, TradeDate: "2025-06-20",
	}); err != nil {
		t.Fatal(err)
	}

	hs, err := svc.ListHoldings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 1 {
		t.Fatalf("holdings = %d, want 1", len(hs))
	}
	if hs[0].Shares != 1300 {
		t.Fatalf("shares = %d, want 1300", hs[0].Shares)
	}
	if got := hs[0].CostPrice; got < 7.68 || got > 7.70 {
		t.Fatalf("cost_price = %.4f, want ≈7.69（送股必须摊薄）", got)
	}
}

func TestService_DividendActionIsFreeAndCountsAsIncome(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	svc.SetClock(fixedClock(2026, 1, 10))
	ctx := context.Background()

	if _, err := svc.CreateAction(ctx, &Action{
		StockCode: "600519", ActionType: ActionBuy, Price: 10, Shares: 1000, TradeDate: "2025-01-10",
	}); err != nil {
		t.Fatal(err)
	}
	a := &Action{StockCode: "600519", ActionType: ActionDividend, Price: 0.35, Shares: 1000, TradeDate: "2025-07-05"}
	if _, err := svc.CreateAction(ctx, a); err != nil {
		t.Fatal(err)
	}
	if a.Fee == nil || *a.Fee != 0 {
		t.Fatalf("fee = %v, want 0（分红不该收手续费）", a.Fee)
	}

	hs, _ := svc.ListHoldings(ctx)
	if hs[0].Shares != 1000 {
		t.Fatalf("shares = %d, want 1000（分红不改股数）", hs[0].Shares)
	}
	if got := hs[0].IncomeRealized; got < 349.9 || got > 350.1 {
		t.Fatalf("income_realized = %.2f, want 350", got)
	}
}

func TestService_DividendEventDilutesCost(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	svc.SetClock(fixedClock(2026, 1, 10))
	ctx := context.Background()

	if _, err := svc.CreateAction(ctx, &Action{
		StockCode: "600519", ActionType: ActionBuy, Price: 10, Shares: 1000, TradeDate: "2025-01-10",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpsertDividendEvent(ctx, &DividendEvent{
		StockCode: "600519", ExDate: "2025-06-20", CashPerShare: 0.40,
	}); err != nil {
		t.Fatal(err)
	}

	hs, _ := svc.ListHoldings(ctx)
	if got := hs[0].CostPrice; got < 9.59 || got > 9.61 {
		t.Fatalf("cost_price = %.4f, want 9.60（除权事件应摊薄）", got)
	}
	if got := hs[0].CostPriceRaw; got < 9.99 || got > 10.01 {
		t.Fatalf("cost_price_raw = %.4f, want 10.00（原值必须保留）", got)
	}
}

// ★ 这条是整个分红模块的核心防线：手工分红流水 + 除权事件同时存在时，
//   绝不能既摊薄成本又计已实现，否则同一笔钱被算两次。
func TestService_ManualDividendSuppressesEventDilution(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	svc.SetClock(fixedClock(2026, 1, 10))
	ctx := context.Background()

	_, _ = svc.CreateAction(ctx, &Action{
		StockCode: "600519", ActionType: ActionBuy, Price: 10, Shares: 1000, TradeDate: "2025-01-10",
	})
	_, _ = svc.CreateAction(ctx, &Action{
		StockCode: "600519", ActionType: ActionDividend, Price: 0.40, Shares: 1000, TradeDate: "2025-06-25",
	})
	_, _ = svc.UpsertDividendEvent(ctx, &DividendEvent{
		StockCode: "600519", ExDate: "2025-06-20", CashPerShare: 0.40,
	})

	hs, _ := svc.ListHoldings(ctx)
	if got := hs[0].CostPrice; got < 9.99 || got > 10.01 {
		t.Fatalf("cost_price = %.4f, want 10.00（已有手工分红流水，不得再摊薄）", got)
	}
	if got := hs[0].IncomeRealized; got < 399.9 || got > 400.1 {
		t.Fatalf("income_realized = %.2f, want 400", got)
	}
	if got := hs[0].DividendPerShare; got != 0 {
		t.Fatalf("dividend_per_share = %.4f, want 0", got)
	}
}

func TestService_DividendEventUpsertIsIdempotent(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	svc.SetClock(fixedClock(2026, 1, 10))
	ctx := context.Background()

	_, _ = svc.CreateAction(ctx, &Action{
		StockCode: "600519", ActionType: ActionBuy, Price: 10, Shares: 1000, TradeDate: "2025-01-10",
	})
	for i := 0; i < 3; i++ {
		if _, err := svc.UpsertDividendEvent(ctx, &DividendEvent{
			StockCode: "600519", ExDate: "2025-06-20", CashPerShare: 0.40,
		}); err != nil {
			t.Fatal(err)
		}
	}
	evs, _ := svc.ListDividendEvents(ctx, "600519")
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1（同一 ex_date 必须去重）", len(evs))
	}
	hs, _ := svc.ListHoldings(ctx)
	if got := hs[0].CostPrice; got < 9.59 || got > 9.61 {
		t.Fatalf("cost_price = %.4f, want 9.60（重复导入不得重复摊薄）", got)
	}
}

func TestService_DividendEventRejectsBadInput(t *testing.T) {
	svc := NewService(newFakeRepo())
	ctx := context.Background()

	cases := []struct {
		name string
		e    DividendEvent
	}{
		{"空代码", DividendEvent{ExDate: "2025-06-20", CashPerShare: 0.4}},
		{"坏日期", DividendEvent{StockCode: "600519", ExDate: "2025/06/20", CashPerShare: 0.4}},
		{"负派息", DividendEvent{StockCode: "600519", ExDate: "2025-06-20", CashPerShare: -1}},
		{"空事件", DividendEvent{StockCode: "600519", ExDate: "2025-06-20"}},
	}
	for _, c := range cases {
		if _, err := svc.UpsertDividendEvent(ctx, &c.e); err == nil {
			t.Fatalf("%s: 期望报错，实际通过", c.name)
		}
	}
}
