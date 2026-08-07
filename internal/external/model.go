// Package external 是场外资产业务域（基金/加密/理财/现金/黄金/机器人）。
package external

import (
	"context"
	"time"
)

// AssetType 场外资产类型。
type AssetType string

const (
	AssetFund   AssetType = "FUND"
	AssetCrypto AssetType = "CRYPTO"
	AssetBot    AssetType = "BOT"
	AssetWealth AssetType = "WEALTH"
	AssetCash   AssetType = "CASH"
	AssetGold   AssetType = "GOLD"
)

// Asset 场外资产。
type Asset struct {
	ID              int64     `json:"id"`
	AssetType       AssetType `json:"asset_type"`
	Code            string    `json:"code"`
	Name            string    `json:"name"`
	Platform        string    `json:"platform"`
	CostAmount      float64   `json:"cost_amount"`
	Shares          *float64  `json:"shares,omitempty"`
	ManualValue     *float64  `json:"manual_value,omitempty"`
	Note            string    `json:"note,omitempty"`
	AnnualYieldRate *float64  `json:"annual_yield_rate,omitempty"`
	StartDate       string    `json:"start_date,omitempty"`
	PendingAmount   float64   `json:"pending_amount"`
	PurchaseFeeRate *float64  `json:"purchase_fee_rate,omitempty"`
	Closed          bool      `json:"closed"`
	ClosedRealized  *float64  `json:"closed_realized,omitempty"`
	ClosedDate      string    `json:"closed_date,omitempty"`
	CreatedAt       string    `json:"created_at"`
	UpdatedAt       string    `json:"updated_at"`
}

// Action 场外流水。
type Action struct {
	ID           int64    `json:"id"`
	AssetID      int64    `json:"asset_id"`
	ActionType   string   `json:"action_type"`
	Amount       float64  `json:"amount"`
	Shares       *float64 `json:"shares,omitempty"`
	UnitPrice    *float64 `json:"unit_price,omitempty"`
	Fee          float64  `json:"fee"`
	TradeDate    string   `json:"trade_date"`
	TradeTime    string   `json:"trade_time,omitempty"`
	Status       string   `json:"status"` // confirmed / pending
	Note         string   `json:"note,omitempty"`
	InterestPart *float64 `json:"interest_part,omitempty"`
	CreatedAt    string   `json:"created_at"`
}

// DCASchedule 定投计划。
type DCASchedule struct {
	ID          int64   `json:"id"`
	AssetID     int64   `json:"asset_id"`
	Mode        string  `json:"mode"` // amount / shares
	Value       float64 `json:"value"`
	Frequency   string  `json:"frequency"` // daily_trading / weekly / monthly
	DayOfMonth  *int    `json:"day_of_month,omitempty"`
	DayOfWeek   *int    `json:"day_of_week,omitempty"`
	Status      string  `json:"status"` // active / paused
	NextDue     string  `json:"next_due,omitempty"`
	LastFiredAt string  `json:"last_fired_at,omitempty"`
	Note        string  `json:"note,omitempty"`
}

// Repository 场外资产持久化接口。
type Repository interface {
	ListAssets(ctx context.Context) ([]Asset, error)
	GetAsset(ctx context.Context, id int64) (*Asset, error)
	CreateAsset(ctx context.Context, a *Asset) (int64, error)
	UpdateAsset(ctx context.Context, a *Asset) error
	DeleteAsset(ctx context.Context, id int64) error

	ListActions(ctx context.Context, assetID int64) ([]Action, error)
	CreateAction(ctx context.Context, a *Action) (int64, error)
	UpdateAction(ctx context.Context, a *Action) error
	DeleteAction(ctx context.Context, id int64) error
	ConfirmAction(ctx context.Context, id int64) error

	ListDCASchedules(ctx context.Context) ([]DCASchedule, error)
	CreateDCASchedule(ctx context.Context, s *DCASchedule) (int64, error)
	UpdateDCASchedule(ctx context.Context, s *DCASchedule) error
	DeleteDCASchedule(ctx context.Context, id int64) error
}

// 保留 time 引用
var _ = time.Time{}
