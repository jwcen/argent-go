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
		{"bad type", &Action{StockCode: "600519", ActionType: "DIVIDEND", Price: 10, Shares: 100}, ErrInvalidAction},
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
