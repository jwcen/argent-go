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
	ActionBuy  ActionType = "BUY"
	ActionSell ActionType = "SELL"
	ActionAdd  ActionType = "ADD" // 红股/送转
)

// Holding 是持仓聚合行。由流水重算得到。
type Holding struct {
	ID           int64   `json:"id"`
	StockCode    string  `json:"stock_code"`
	StockName    string  `json:"stock_name"`
	Shares       int64   `json:"shares"`
	CostPrice    float64 `json:"cost_price"`
	PurchaseDate string  `json:"purchase_date"`
	Broker       string  `json:"broker"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
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
}
