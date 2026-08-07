package external

import (
	"context"
	"fmt"
	"time"
)

// Service 场外资产业务层。
type Service struct {
	repo Repository
	now  func() time.Time
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo, now: time.Now}
}

func (s *Service) SetClock(f func() time.Time) { s.now = f }

// ---- Assets ----

func (s *Service) ListAssets(ctx context.Context) ([]Asset, error) {
	return s.repo.ListAssets(ctx)
}

func (s *Service) CreateAsset(ctx context.Context, a *Asset) (int64, error) {
	if a.Code == "" || a.Name == "" {
		return 0, ErrInvalidType
	}
	return s.repo.CreateAsset(ctx, a)
}

func (s *Service) UpdateAsset(ctx context.Context, a *Asset) error {
	return s.repo.UpdateAsset(ctx, a)
}

func (s *Service) DeleteAsset(ctx context.Context, id int64) error {
	return s.repo.DeleteAsset(ctx, id)
}

// ---- Actions ----

func (s *Service) ListActions(ctx context.Context, assetID int64) ([]Action, error) {
	return s.repo.ListActions(ctx, assetID)
}

// AddLot 加仓：写一条 BUY 流水，重算 asset 的 cost_amount 和 shares。
func (s *Service) AddLot(ctx context.Context, assetID int64, a *Action) (int64, error) {
	a.AssetID = assetID
	a.ActionType = "BUY"
	if a.Status == "" {
		a.Status = "confirmed"
	}
	if a.TradeDate == "" {
		a.TradeDate = s.now().Format("2006-01-02")
	}

	id, err := s.repo.CreateAction(ctx, a)
	if err != nil {
		return 0, err
	}

	if a.Status == "confirmed" {
		if err := s.recomputeAsset(ctx, assetID); err != nil {
			return id, fmt.Errorf("external: recompute: %w", err)
		}
	}
	return id, nil
}

// ReduceLot 减仓：写一条 REDEEM 流水。
func (s *Service) ReduceLot(ctx context.Context, assetID int64, a *Action) (int64, error) {
	a.AssetID = assetID
	a.ActionType = "REDEEM"
	if a.Status == "" {
		a.Status = "confirmed"
	}
	if a.TradeDate == "" {
		a.TradeDate = s.now().Format("2006-01-02")
	}

	id, err := s.repo.CreateAction(ctx, a)
	if err != nil {
		return 0, err
	}
	if a.Status == "confirmed" {
		if err := s.recomputeAsset(ctx, assetID); err != nil {
			return id, fmt.Errorf("external: recompute: %w", err)
		}
	}
	return id, nil
}

// ConfirmAction 确认 pending 流水（T+1 份额结算）。
func (s *Service) ConfirmAction(ctx context.Context, id int64) error {
	action, err := s.findAction(ctx, id)
	if err != nil {
		return err
	}
	if action.Status != "pending" {
		return ErrNotPending
	}
	if err := s.repo.ConfirmAction(ctx, id); err != nil {
		return err
	}
	return s.recomputeAsset(ctx, action.AssetID)
}

func (s *Service) DeleteAction(ctx context.Context, id int64) error {
	action, err := s.findAction(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteAction(ctx, id); err != nil {
		return err
	}
	return s.recomputeAsset(ctx, action.AssetID)
}

// recomputeAsset 读全部流水重算 asset 的 cost_amount / shares。
func (s *Service) recomputeAsset(ctx context.Context, assetID int64) error {
	asset, err := s.repo.GetAsset(ctx, assetID)
	if err != nil {
		return err
	}

	actions, err := s.repo.ListActions(ctx, assetID)
	if err != nil {
		return err
	}

	var totalCost, totalShares float64
	for _, a := range actions {
		if a.Status != "confirmed" {
			continue
		}
		switch a.ActionType {
		case "BUY", "ADD", "DEPOSIT":
			totalCost += a.Amount
			if a.Shares != nil {
				totalShares += *a.Shares
			}
		case "REDEEM", "WITHDRAW":
			totalCost -= a.Amount
			if a.Shares != nil {
				totalShares -= *a.Shares
			}
		}
	}

	asset.CostAmount = totalCost
	if totalShares > 0 {
		v := totalShares
		asset.Shares = &v
	} else {
		asset.Shares = nil
	}

	return s.repo.UpdateAsset(ctx, asset)
}

func (s *Service) findAction(ctx context.Context, id int64) (*Action, error) {
	// 简化：遍历所有 asset 找 action。生产环境可加 GetActionByID 到 Repository。
	assets, err := s.repo.ListAssets(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range assets {
		actions, err := s.repo.ListActions(ctx, a.ID)
		if err != nil {
			continue
		}
		for _, act := range actions {
			if act.ID == id {
				return &act, nil
			}
		}
	}
	return nil, ErrNotFound
}

// ---- DCA ----

func (s *Service) ListDCA(ctx context.Context) ([]DCASchedule, error) {
	return s.repo.ListDCASchedules(ctx)
}

func (s *Service) CreateDCA(ctx context.Context, d *DCASchedule) (int64, error) {
	if d.Status == "" {
		d.Status = "active"
	}
	return s.repo.CreateDCASchedule(ctx, d)
}

func (s *Service) UpdateDCA(ctx context.Context, d *DCASchedule) error {
	return s.repo.UpdateDCASchedule(ctx, d)
}

func (s *Service) DeleteDCA(ctx context.Context, id int64) error {
	return s.repo.DeleteDCASchedule(ctx, id)
}
