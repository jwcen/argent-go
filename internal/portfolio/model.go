// Package portfolio 是 A 股持仓业务域。
//
// 架构铁律（与 auth 域一致）：
//   - 本包是业务域（内层），只依赖标准库 + internal/ledger + internal/domain。
//   - 不知道 gin、不知道 SQLite——数据访问经本包定义的 Repository 接口，
//     由 internal/infra/sqlite 提供实现。
//   - 任何流水增删改后，用 ledger.ComputePositionState 重算聚合写回 holdings。
package portfolio

import (
	"context"
)

// ActionType 持仓动作类型，与 ledger.ActionType 对齐。
type ActionType string

const (
	ActionBuy      ActionType = "BUY"
	ActionSell     ActionType = "SELL"
	ActionAdd      ActionType = "ADD"      // 增股（人工补录）
	ActionBonus    ActionType = "BONUS"    // 送股/转增，price 恒为 0，被动摊薄成本
	ActionDividend ActionType = "DIVIDEND" // 现金分红，price=每股派息，不改股数
)

// Holding 是持仓聚合行。由流水重算得到。
//
// 注意 cost_price 是「已摊薄」的成本；cost_price_raw 是摊薄前的原值。
// 前端 tooltip 用两者之差解释"为什么我的成本比买入价低"。
// 这三个衍生字段不落库（holdings 表沿用原版 schema 保证兼容），
// 每次读取时由 service 从流水 + 除权事件实时算出。
type Holding struct {
	ID           int64   `json:"id"`
	StockCode    string  `json:"stock_code"`
	StockName    string  `json:"stock_name"`
	Shares       int64   `json:"shares"`
	CostPrice    float64 `json:"cost_price"`
	PurchaseDate string  `json:"purchase_date"`
	Broker       string  `json:"broker"`
	AccountID    *int64  `json:"account_id,omitempty"` // 归属账户（NULL=未归类）
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`

	// ---- 衍生字段（不落库）----
	CostPriceRaw     float64 `json:"cost_price_raw,omitempty"`     // 摊薄前成本
	FIFOCostPrice    float64 `json:"fifo_cost_price,omitempty"`    // FIFO 成本法单价
	DividendPerShare float64 `json:"dividend_per_share,omitempty"` // 已摊薄的每股派息
	IncomeRealized   float64 `json:"income_realized,omitempty"`    // 累计现金分红收入
	RealizedCarry    float64 `json:"realized_carry,omitempty"`     // 可直接加进总盈亏的部分
	WeightedDays     int     `json:"weighted_days,omitempty"`      // 加权持有天数
}

// DividendEvent 是一次客观的除权除息事件。
//
// 与「用户手工记的 DIVIDEND 流水」是两码事：
//   - 本表 → 摊薄成本（DiluteState）
//   - 流水 → 计入已实现收益（income_realized）
//
// 两条路径互斥，服务层不会让同一笔钱被算两次。
type DividendEvent struct {
	ID           int64   `json:"id"`
	StockCode    string  `json:"stock_code"`
	ExDate       string  `json:"ex_date"`        // YYYY-MM-DD
	CashPerShare float64 `json:"cash_per_share"` // 每股派息（元，含税）
	BonusRatio   float64 `json:"bonus_ratio"`    // 每股送转率
	Source       string  `json:"source"`         // manual / eastmoney
	Note         string  `json:"note"`
	CreatedAt    string  `json:"created_at"`
}

// Action 是一笔持仓流水——业务数据的真相源。
type Action struct {
	ID         int64      `json:"id"`
	StockCode  string     `json:"stock_code"`
	ActionType ActionType `json:"action_type"`
	Price      float64    `json:"price"`
	Shares     int64      `json:"shares"`
	TrancheID  *int64     `json:"tranche_id,omitempty"`
	Note       string     `json:"note"`
	TradeDate  string     `json:"trade_date"`
	TradeTime  string     `json:"trade_time,omitempty"`
	Fee        *float64   `json:"fee,omitempty"`
	Broker     string     `json:"broker,omitempty"`
	AccountID  *int64     `json:"account_id,omitempty"` // 归属账户（NULL=未归类）
	CreatedAt  string     `json:"created_at"`
}

// Broker 是券商费率配置。
type Broker struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	StockRate float64 `json:"stock_rate"`
	StockMin  float64 `json:"stock_min"`
	EtfRate   float64 `json:"etf_rate"`
	EtfMin    float64 `json:"etf_min"`
	IsDefault bool    `json:"is_default"`
}

// Thesis 是买入逻辑。
type Thesis struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Thesis    string `json:"thesis"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// WatchlistItem 是自选股。
type WatchlistItem struct {
	StockCode  string   `json:"stock_code"`
	StockName  string   `json:"stock_name"`
	AddedAt    string   `json:"added_at"`
	AddedPrice *float64 `json:"added_price,omitempty"`
}

// RealizedResult 是单只股票的已实现盈亏。
type RealizedResult struct {
	StockCode     string  `json:"stock_code"`
	StockName     string  `json:"stock_name"`
	RealizedPnL   float64 `json:"realized_pnl"`
	RealizedCarry float64 `json:"realized_carry"`
}

// AccountKind 账户类型枚举。
type AccountKind string

const (
	AccountStock  AccountKind = "stock"  // A 股证券账户
	AccountFund   AccountKind = "fund"   // 基金平台（天天基金/支付宝等）
	AccountBank   AccountKind = "bank"   // 银行理财
	AccountCustom AccountKind = "custom" // 自定义
)

// Account 是用户自定义的投资账户分组。
//
// 一个用户可以创建多个账户（如「华泰证券」「支付宝」「天天基金」），
// 持仓和流水通过 account_id 归属到具体账户，实现按账户统计汇总。
type Account struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Kind      AccountKind `json:"kind"`
	Color     string     `json:"color,omitempty"` // 可选颜色标签
	SortOrder int        `json:"sort_order"`
	CreatedAt string     `json:"created_at"`
}

// AccountSummary 是单个账户的持仓汇总快照。
type AccountSummary struct {
	AccountID    int64   `json:"account_id"`
	AccountName  string  `json:"account_name"`
	HoldingCount int     `json:"holding_count"`
	TotalCost    float64 `json:"total_cost"`
	TotalShares  int64   `json:"total_shares"`
}

// Repository 抽象 portfolio 所需的全部持久化操作。
type Repository interface {
	ListHoldings(ctx context.Context) ([]Holding, error)
	GetHolding(ctx context.Context, code string) (*Holding, error)
	UpsertHolding(ctx context.Context, h *Holding) error
	DeleteHolding(ctx context.Context, code string) error

	ListActions(ctx context.Context, code string) ([]Action, error)
	ListAllActions(ctx context.Context) ([]Action, error)
	// GetAction 按主键取单条流水；不存在时返回 (nil, nil)。
	GetAction(ctx context.Context, id int64) (*Action, error)
	CreateAction(ctx context.Context, a *Action) (int64, error)
	UpdateAction(ctx context.Context, a *Action) error
	DeleteAction(ctx context.Context, id int64) error

	ListBrokers(ctx context.Context) ([]Broker, error)
	GetDefaultBroker(ctx context.Context) (*Broker, error)
	CreateBroker(ctx context.Context, b *Broker) (int64, error)
	UpdateBroker(ctx context.Context, b *Broker) error
	DeleteBroker(ctx context.Context, id int64) error

	GetThesis(ctx context.Context, code string) (*Thesis, error)
	UpsertThesis(ctx context.Context, t *Thesis) error
	DeleteThesis(ctx context.Context, code string) error

	ListWatchlist(ctx context.Context) ([]WatchlistItem, error)
	AddWatchlist(ctx context.Context, w *WatchlistItem) error
	RemoveWatchlist(ctx context.Context, code string) error

	ListDividendEvents(ctx context.Context, code string) ([]DividendEvent, error)
	ListAllDividendEvents(ctx context.Context) ([]DividendEvent, error)
	// UpsertDividendEvent 按 (stock_code, ex_date) 幂等写入，重复导入不会产生双份摊薄。
	UpsertDividendEvent(ctx context.Context, e *DividendEvent) (int64, error)
	DeleteDividendEvent(ctx context.Context, id int64) error

	// ---------- Accounts ----------
	ListAccounts(ctx context.Context) ([]Account, error)
	GetAccount(ctx context.Context, id int64) (*Account, error)
	CreateAccount(ctx context.Context, a *Account) (int64, error)
	UpdateAccount(ctx context.Context, a *Account) error
	DeleteAccount(ctx context.Context, id int64) error
	// ListHoldingsByAccount 按 account_id 过滤持仓（id=0 或 NULL 表示未归类）。
	ListHoldingsByAccount(ctx context.Context, accountID int64) ([]Holding, error)
	// AccountSummaryByAll 返回每个账户的持仓汇总（含「未归类」组）。
	AccountSummaries(ctx context.Context) ([]AccountSummary, error)
}
