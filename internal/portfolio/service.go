package portfolio

import (
	"context"
	"fmt"
	"time"

	"github.com/jwcen/argent-go/internal/domain"
	"github.com/jwcen/argent-go/internal/ledger"
)

// NameResolver 从外部数据源（如行情接口）根据代码查询股票名称。
// 查不到或源不可达时返回空字符串（不报错，优雅降级）。
type NameResolver func(ctx context.Context, code string) string

// Service 是 portfolio 域的用例层。
//
// 它编排 Repository（持久化）和 ledger（纯计算）：
//   - 流水增删改 → 读全部流水 → ledger 重算 → 写回 holdings 聚合
//   - 手续费自动估算（fee 为 NULL 时用 EstimateTradeFee）
//
// 不 import gin / sqlite，可被单测直接调用（fake repo + 可注入时钟）。
type Service struct {
	repo         Repository
	now          func() time.Time
	nameResolver NameResolver // 可选：首次建 holding 时自动查名
}

// NewService 构造 portfolio 服务。
func NewService(repo Repository) *Service {
	return &Service{repo: repo, now: time.Now}
}

// SetNameResolver 注入名称解析器（可选，不注入则名称保持为空）。
func (s *Service) SetNameResolver(r NameResolver) { s.nameResolver = r }

// SetClock 注入时钟（测试用）。
func (s *Service) SetClock(f func() time.Time) { s.now = f }

// ---- Holdings ----

// ListHoldings 返回全部持仓聚合行，并补齐分红衍生字段。
//
// 为什么衍生字段在读取时算、而不是落库：
//   - 摊薄依赖「今天」（ex_date <= today）和除权事件表，两者都会变，落库就会过期；
//   - holdings 表要与原版 Python schema 保持字节级兼容，不能加列。
//
// 成本是 3 次查询（holdings + 全部流水 + 全部除权事件），不随持仓数量放大。
func (s *Service) ListHoldings(ctx context.Context) ([]Holding, error) {
	holdings, err := s.repo.ListHoldings(ctx)
	if err != nil {
		return nil, err
	}
	return s.enrichHoldings(ctx, holdings)
}

// enrichHoldings 为持仓列表补齐分红衍生字段（成本摊薄/已实现收益等）。
// 入参 holdings 可以是全部持仓或按账户过滤后的子集。
func (s *Service) enrichHoldings(ctx context.Context, holdings []Holding) ([]Holding, error) {
	if len(holdings) == 0 {
		return holdings, nil
	}

	actions, err := s.repo.ListAllActions(ctx)
	if err != nil {
		return holdings, nil // 降级：拿不到流水就返回裸持仓，不要让整个页面挂掉
	}
	events, err := s.repo.ListAllDividendEvents(ctx)
	if err != nil {
		events = nil
	}

	actsByCode := make(map[string][]Action, len(holdings))
	for _, a := range actions {
		actsByCode[a.StockCode] = append(actsByCode[a.StockCode], a)
	}
	evByCode := make(map[string][]DividendEvent)
	for _, e := range events {
		evByCode[e.StockCode] = append(evByCode[e.StockCode], e)
	}

	today := s.now()
	for i := range holdings {
		code := holdings[i].StockCode

		// 名称补填：首次录入时可能没查到名称，这里在读取时补救
		if holdings[i].StockName == "" && s.nameResolver != nil {
			if resolved := s.nameResolver(ctx, code); resolved != "" {
				holdings[i].StockName = resolved
				// 异步写回 DB，下次不用再查
				go func(c string, n string) {
					ctx2, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if h, err := s.repo.GetHolding(ctx2, c); err == nil && h != nil {
						h.StockName = n
						_ = s.repo.UpsertHolding(ctx2, h)
					}
				}(code, resolved)
			}
		}

		acts := actsByCode[code]
		if len(acts) == 0 {
			continue
		}
		state := ledger.ComputePositionState(toLedgerActions(acts), today)
		applyDilution(&state, acts, evByCode[code], today)

		holdings[i].CostPrice = state.CostPrice.YuanF()
		holdings[i].CostPriceRaw = state.CostPriceRaw.YuanF()
		holdings[i].FIFOCostPrice = state.FIFOCostPrice.YuanF()
		holdings[i].DividendPerShare = state.DividendPerShare.YuanF()
		holdings[i].IncomeRealized = state.IncomeRealized.YuanF()
		holdings[i].RealizedCarry = state.RealizedCarry.YuanF()
		holdings[i].WeightedDays = state.WeightedDays
	}
	return holdings, nil
}

// applyDilution 决定「这只股票要不要用除权事件摊薄成本」，并执行摊薄。
//
// ★ 防双计规则（整个分红模块最关键的一条）★
//
// 现金分红这笔钱只能被算一次。它有两种记法，二选一：
//
//	记法 A —— 手工记一笔 DIVIDEND 流水。
//	          钱进 income_realized，算作「已落袋的收益」，成本价不变。
//	记法 B —— 依赖 dividend_events 里的客观除权事件。
//	          成本价被摊低，浮动盈亏因此变大，不进已实现。
//
// 两种记法对「总收益」的贡献是等价的，但如果同时生效，
// 同一笔分红会既抬高浮动盈亏、又抬高已实现盈亏，总收益凭空翻倍。
//
// 所以规则是：**只要这只股票存在任何手工 DIVIDEND 流水，就认为用户选了记法 A，
// 完全跳过事件摊薄。** 不做 ex_date 逐笔配对——分红流水的 trade_date 通常是
// 到账日而非除权日，日期对不上，按日期配对必然漏配或错配，反而制造隐蔽的错账。
// 宁可用一条粗但可预测的规则，也不要一条精细但会静默出错的规则。
func applyDilution(state *ledger.PositionState, acts []Action, events []DividendEvent, today time.Time) {
	for _, a := range acts {
		if a.ActionType == ActionDividend {
			return // 用户走的是记法 A，不再摊薄
		}
	}
	if len(events) == 0 {
		return
	}
	div := ledger.CumulativeCashDivPerShare(toLedgerEvents(events), state.OpenedAt, today)
	if div > 0 {
		ledger.DiluteState(state, div)
	}
}

// ---- Dividend events ----

// ListDividendEvents 返回某只股票的全部除权事件。
func (s *Service) ListDividendEvents(ctx context.Context, code string) ([]DividendEvent, error) {
	if code == "" {
		return nil, ErrInvalidCode
	}
	return s.repo.ListDividendEvents(ctx, code)
}

// UpsertDividendEvent 新增或覆盖一次除权事件（按 code + ex_date 幂等）。
func (s *Service) UpsertDividendEvent(ctx context.Context, e *DividendEvent) (int64, error) {
	if e.StockCode == "" {
		return 0, ErrInvalidCode
	}
	if _, err := time.Parse(dateLayout, e.ExDate); err != nil {
		return 0, ErrInvalidAction
	}
	if e.CashPerShare < 0 || e.BonusRatio < 0 {
		return 0, ErrInvalidPrice
	}
	if e.CashPerShare == 0 && e.BonusRatio == 0 {
		return 0, ErrInvalidAction // 空事件没有意义
	}
	if e.Source == "" {
		e.Source = "manual"
	}
	return s.repo.UpsertDividendEvent(ctx, e)
}

// DeleteDividendEvent 删除一次除权事件。
func (s *Service) DeleteDividendEvent(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrNotFound
	}
	return s.repo.DeleteDividendEvent(ctx, id)
}

// ---- Actions ----

// ListActions 返回某只股票的全部流水。
func (s *Service) ListActions(ctx context.Context, code string) ([]Action, error) {
	if code == "" {
		return nil, ErrInvalidCode
	}
	return s.repo.ListActions(ctx, code)
}

// ListAllActions 列出当前用户全部成交流水（不区分标的）。供 agent 工具"不传 code = 全部"使用。
func (s *Service) ListAllActions(ctx context.Context) ([]Action, error) {
	return s.repo.ListAllActions(ctx)
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

	if err := s.recomputeHolding(ctx, a.StockCode, a.AccountID); err != nil {
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
	if err := s.recomputeHolding(ctx, a.StockCode, a.AccountID); err != nil {
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
	if err := s.recomputeHolding(ctx, code, nil); err != nil {
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
// overrideAccountID 非 nil 时优先用作该持仓的归属账户（首次创建/编辑流水时指定），
// 否则从现有 holding 保留归属，避免重算时丢掉用户选的账户分组。
func (s *Service) recomputeHolding(ctx context.Context, code string, overrideAccountID *int64) error {
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

	// 取 stock_name 和 account_id（从现有 holding 保留归属）
	name := ""
	var accountID *int64
	if existing, _ := s.repo.GetHolding(ctx, code); existing != nil {
		name = existing.StockName
		accountID = existing.AccountID
	}
	// 调用方（新增/编辑流水）明确指定了归属账户时优先采用，
	// 否则沿用现有持仓的账户（首次建持仓且未指定时为空=未归类）。
	if overrideAccountID != nil {
		accountID = overrideAccountID
	}
	if name == "" && s.nameResolver != nil {
		// 首次录入或名称丢失：从行情源自动查询
		if resolved := s.nameResolver(ctx, code); resolved != "" {
			name = resolved
		}
	}

	var purchaseDate string
	if len(actions) > 0 {
		purchaseDate = actions[0].TradeDate
	}

	// 落库存的是**未摊薄**的原始成本。摊薄依赖「今天」和除权事件表，两者都会随时间变，
	// 存成快照就会过期；统一在 ListHoldings 读取时实时摊薄（与原版 Python 的
	// compute_position_state → dilute_state 调用链同口径）。
	h := &Holding{
		StockCode:    code,
		StockName:    name,
		Shares:       state.Shares,
		CostPrice:    state.CostPriceRaw.YuanF(),
		PurchaseDate: purchaseDate,
		AccountID:    accountID,
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

// dateLayout 是流水/事件里日期列的统一格式。
const dateLayout = "2006-01-02"

// toLedgerEvents 把 portfolio.DividendEvent 转成 ledger.DividendEvent。
// 日期解析失败的事件直接丢弃——脏数据参与摊薄比不摊薄危险得多。
func toLedgerEvents(events []DividendEvent) []ledger.DividendEvent {
	out := make([]ledger.DividendEvent, 0, len(events))
	for _, e := range events {
		d, err := time.Parse(dateLayout, e.ExDate)
		if err != nil {
			continue
		}
		out = append(out, ledger.DividendEvent{
			ExDate:       d,
			CashPerShare: domain.Yuan(e.CashPerShare),
			BonusRatio:   e.BonusRatio,
		})
	}
	return out
}

// toLedgerActions 把 portfolio.Action 转成 ledger.Action。
func toLedgerActions(actions []Action) []ledger.Action {
	out := make([]ledger.Action, 0, len(actions))
	for _, a := range actions {
		t, err := time.Parse(dateLayout, a.TradeDate)
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

// ---- Accounts ----

func (s *Service) ListAccounts(ctx context.Context) ([]Account, error) {
	return s.repo.ListAccounts(ctx)
}

func (s *Service) CreateAccount(ctx context.Context, a *Account) (int64, error) {
	if a.Name == "" {
		return 0, ErrInvalidCode
	}
	// 默认 kind 为 custom
	if a.Kind == "" {
		a.Kind = AccountCustom
	}
	return s.repo.CreateAccount(ctx, a)
}

func (s *Service) UpdateAccount(ctx context.Context, a *Account) error {
	if a.ID == 0 {
		return ErrNotFound
	}
	return s.repo.UpdateAccount(ctx, a)
}

func (s *Service) DeleteAccount(ctx context.Context, id int64) error {
	return s.repo.DeleteAccount(ctx, id)
}

// ListHoldingsByAccount 按 account_id 过滤持仓（0=未归类）。
func (s *Service) ListHoldingsByAccount(ctx context.Context, accountID int64) ([]Holding, error) {
	holdings, err := s.repo.ListHoldingsByAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return s.enrichHoldings(ctx, holdings)
}

// AccountSummaries 返回每个账户的持仓汇总。
func (s *Service) AccountSummaries(ctx context.Context) ([]AccountSummary, error) {
	return s.repo.AccountSummaries(ctx)
}

func validateAction(a *Action) error {
	if a.StockCode == "" {
		return ErrInvalidCode
	}
	switch a.ActionType {
	case ActionBuy, ActionSell, ActionAdd, ActionBonus, ActionDividend:
	default:
		return ErrInvalidAction
	}
	if a.Price < 0 {
		return ErrInvalidPrice
	}
	// 送股的 price 必须是 0：非零会被 FIFO 当成有成本的批次，
	// 摊薄效果直接失效（这是最容易记错的一个口径）。
	if a.ActionType == ActionBonus {
		a.Price = 0
	}
	if a.Shares <= 0 {
		return ErrInvalidShares
	}
	return nil
}
