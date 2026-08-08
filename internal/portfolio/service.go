package portfolio

import (
	"context"
	"fmt"
	"time"

	"github.com/jwcen/argent-go/internal/domain"
	"github.com/jwcen/argent-go/internal/ledger"
)

// Service 是 portfolio 域的用例层。
//
// 它编排 Repository（持久化）和 ledger（纯计算）：
//   - 流水增删改 → 读全部流水 → ledger 重算 → 写回 holdings 聚合
//   - 手续费自动估算（fee 为 NULL 时用 EstimateTradeFee）
//
// 不 import gin / sqlite，可被单测直接调用（fake repo + 可注入时钟）。
type Service struct {
	repo Repository
	now  func() time.Time
}

// NewService 构造 portfolio 服务。
func NewService(repo Repository) *Service {
	return &Service{repo: repo, now: time.Now}
}

// SetClock 注入时钟（测试用）。
func (s *Service) SetClock(f func() time.Time) { s.now = f }

// ---- Holdings ----

// ListHoldings 返回全部持仓聚合行。
func (s *Service) ListHoldings(ctx context.Context) ([]Holding, error) {
	return s.repo.ListHoldings(ctx)
}

// ---- Actions ----

// ListActions 返回某只股票的全部流水。
func (s *Service) ListActions(ctx context.Context, code string) ([]Action, error) {
	if code == "" {
		return nil, ErrInvalidCode
	}
	return s.repo.ListActions(ctx, code)
}

// GetAction 按主键取单条流水；不存在时返回 (nil, nil)。
func (s *Service) GetAction(ctx context.Context, id int64) (*Action, error) {
	if id <= 0 {
		return nil, ErrNotFound
	}
	return s.repo.GetAction(ctx, id)
}

// CreateAction 创建一笔流水并自动重算持仓聚合。
//
// 若 Fee 为 nil，用 broker 费率自动估算（无 broker 则用默认费率）。
// 若 Broker 为空但 action.Broker 有值，尝试从 DB 取该 broker 的费率。
func (s *Service) CreateAction(ctx context.Context, a *Action) (int64, error) {
	if err := validateAction(a); err != nil {
		return 0, err
	}
	s.fillFee(ctx, a)

	id, err := s.repo.CreateAction(ctx, a)
	if err != nil {
		return 0, err
	}
	a.ID = id

	if err := s.recomputeHolding(ctx, a.StockCode); err != nil {
		return id, fmt.Errorf("portfolio: recompute after create action: %w", err)
	}
	return id, nil
}

// UpdateAction 修改一笔流水并重算。
func (s *Service) UpdateAction(ctx context.Context, a *Action) error {
	if a.ID == 0 {
		return ErrInvalidAction
	}
	if err := validateAction(a); err != nil {
		return err
	}
	s.fillFee(ctx, a)

	if err := s.repo.UpdateAction(ctx, a); err != nil {
		return err
	}
	if err := s.recomputeHolding(ctx, a.StockCode); err != nil {
		return fmt.Errorf("portfolio: recompute after update action: %w", err)
	}
	return nil
}

// DeleteAction 删除一笔流水并重算。
func (s *Service) DeleteAction(ctx context.Context, id int64) error {
	// 先查出 stock_code，重算需要它
	actions, err := s.repo.ListAllActions(ctx)
	if err != nil {
		return err
	}
	var code string
	for _, a := range actions {
		if a.ID == id {
			code = a.StockCode
			break
		}
	}
	if code == "" {
		return ErrNotFound
	}

	if err := s.repo.DeleteAction(ctx, id); err != nil {
		return err
	}
	if err := s.recomputeHolding(ctx, code); err != nil {
		return fmt.Errorf("portfolio: recompute after delete action: %w", err)
	}
	return nil
}

// ---- Realized ----

// Realized 返回全部已实现盈亏（按股票分组）。
func (s *Service) Realized(ctx context.Context) ([]RealizedResult, error) {
	actions, err := s.repo.ListAllActions(ctx)
	if err != nil {
		return nil, err
	}

	// 按 stock_code 分组
	groups := make(map[string][]Action)
	for _, a := range actions {
		groups[a.StockCode] = append(groups[a.StockCode], a)
	}

	results := make([]RealizedResult, 0, len(groups))
	for code, acts := range groups {
		ledgerActs := toLedgerActions(acts)
		state := ledger.ComputePositionState(ledgerActs, s.now())

		h, _ := s.repo.GetHolding(ctx, code)
		name := ""
		if h != nil {
			name = h.StockName
		}

		results = append(results, RealizedResult{
			StockCode:     code,
			StockName:     name,
			RealizedPnL:   state.RealizedPnL.YuanF(),
			RealizedCarry: state.RealizedCarry.YuanF(),
		})
	}
	return results, nil
}

// ---- Brokers ----

func (s *Service) ListBrokers(ctx context.Context) ([]Broker, error) {
	return s.repo.ListBrokers(ctx)
}

func (s *Service) CreateBroker(ctx context.Context, b *Broker) (int64, error) {
	if b.Name == "" {
		return 0, ErrInvalidCode
	}
	if b.StockRate <= 0 {
		b.StockRate = defaultCommissionRate
	}
	if b.StockMin <= 0 {
		b.StockMin = defaultCommissionMin
	}
	return s.repo.CreateBroker(ctx, b)
}

func (s *Service) UpdateBroker(ctx context.Context, b *Broker) error {
	if b.ID == 0 {
		return ErrNotFound
	}
	return s.repo.UpdateBroker(ctx, b)
}

func (s *Service) DeleteBroker(ctx context.Context, id int64) error {
	return s.repo.DeleteBroker(ctx, id)
}

// ---- Thesis ----

func (s *Service) GetThesis(ctx context.Context, code string) (*Thesis, error) {
	return s.repo.GetThesis(ctx, code)
}

func (s *Service) UpsertThesis(ctx context.Context, t *Thesis) error {
	if t.Code == "" {
		return ErrInvalidCode
	}
	return s.repo.UpsertThesis(ctx, t)
}

func (s *Service) DeleteThesis(ctx context.Context, code string) error {
	return s.repo.DeleteThesis(ctx, code)
}

// ---- Watchlist ----

func (s *Service) ListWatchlist(ctx context.Context) ([]WatchlistItem, error) {
	return s.repo.ListWatchlist(ctx)
}

func (s *Service) AddWatchlist(ctx context.Context, w *WatchlistItem) error {
	if w.StockCode == "" {
		return ErrInvalidCode
	}
	if w.AddedAt == "" {
		w.AddedAt = s.now().Format("2006-01-02")
	}
	return s.repo.AddWatchlist(ctx, w)
}

func (s *Service) RemoveWatchlist(ctx context.Context, code string) error {
	return s.repo.RemoveWatchlist(ctx, code)
}

// ---- internal ----

// recomputeHolding 读全部流水 → ledger 重算 → 写回 holdings。
func (s *Service) recomputeHolding(ctx context.Context, code string) error {
	actions, err := s.repo.ListActions(ctx, code)
	if err != nil {
		return err
	}

	ledgerActs := toLedgerActions(actions)
	state := ledger.ComputePositionState(ledgerActs, s.now())

	if state.Shares == 0 {
		// 清仓：删除持仓行（流水保留）
		return s.repo.DeleteHolding(ctx, code)
	}

	// 取 stock_name（从现有 holding 或第一条流水推断）
	name := ""
	if h, _ := s.repo.GetHolding(ctx, code); h != nil {
		name = h.StockName
	}
	if name == "" && len(actions) > 0 {
		// 从流水里无法直接拿 name，保持空
	}

	var purchaseDate string
	if len(actions) > 0 {
		purchaseDate = actions[0].TradeDate
	}

	h := &Holding{
		StockCode:    code,
		StockName:    name,
		Shares:       state.Shares,
		CostPrice:    state.CostPrice.YuanF(),
		PurchaseDate: purchaseDate,
	}
	return s.repo.UpsertHolding(ctx, h)
}

// fillFee 在 Fee 为 nil 时自动估算手续费。
func (s *Service) fillFee(ctx context.Context, a *Action) {
	if a.Fee != nil {
		return
	}
	var b *Broker
	if a.Broker != "" {
		brokers, _ := s.repo.ListBrokers(ctx)
		for i := range brokers {
			if brokers[i].Name == a.Broker {
				b = &brokers[i]
				break
			}
		}
	}
	fee := EstimateTradeFee(a.ActionType, a.Price, a.Shares, b)
	a.Fee = &fee
}

// toLedgerActions 把 portfolio.Action 转成 ledger.Action。
func toLedgerActions(actions []Action) []ledger.Action {
	out := make([]ledger.Action, 0, len(actions))
	for _, a := range actions {
		t, err := time.Parse("2006-01-02", a.TradeDate)
		if err != nil {
			t = time.Now()
		}
		out = append(out, ledger.Action{
			Type:      ledger.ActionType(a.ActionType),
			Price:     domain.Yuan(a.Price),
			Shares:    a.Shares,
			TradeDate: t,
		})
	}
	return out
}

func validateAction(a *Action) error {
	if a.StockCode == "" {
		return ErrInvalidCode
	}
	switch a.ActionType {
	case ActionBuy, ActionSell, ActionAdd:
	default:
		return ErrInvalidAction
	}
	if a.Price < 0 {
		return ErrInvalidPrice
	}
	if a.Shares <= 0 {
		return ErrInvalidShares
	}
	return nil
}
