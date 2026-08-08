package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jwcen/argent-go/internal/external"
)

type ExternalRepo struct{ db *sql.DB }

func NewExternalRepo(db *sql.DB) *ExternalRepo { return &ExternalRepo{db: db} }

var _ external.Repository = (*ExternalRepo)(nil)

func (r *ExternalRepo) ListAssets(ctx context.Context) ([]external.Asset, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, asset_type, code, name, platform, cost_amount, shares, manual_value,
		        note, annual_yield_rate, start_date, pending_amount, purchase_fee_rate,
		        closed, closed_realized, closed_date, created_at, updated_at
		 FROM external_assets WHERE closed = 0 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list ext assets: %w", err)
	}
	defer rows.Close()
	return scanAssets(rows)
}

func (r *ExternalRepo) GetAsset(ctx context.Context, id int64) (*external.Asset, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, asset_type, code, name, platform, cost_amount, shares, manual_value,
		        note, annual_yield_rate, start_date, pending_amount, purchase_fee_rate,
		        closed, closed_realized, closed_date, created_at, updated_at
		 FROM external_assets WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets, err := scanAssets(rows)
	if err != nil {
		return nil, err
	}
	if len(assets) == 0 {
		return nil, external.ErrNotFound
	}
	return &assets[0], nil
}

func scanAssets(rows *sql.Rows) ([]external.Asset, error) {
	out := make([]external.Asset, 0)
	for rows.Next() {
		var a external.Asset
		var platform, note, startDate, closedDate sql.NullString
		var shares, manualValue, annualYield, purchaseFee, closedRealized sql.NullFloat64
		var closed int
		var createdAt, updatedAt string
		if err := rows.Scan(&a.ID, &a.AssetType, &a.Code, &a.Name, &platform,
			&a.CostAmount, &shares, &manualValue, &note, &annualYield, &startDate,
			&a.PendingAmount, &purchaseFee, &closed, &closedRealized, &closedDate,
			&createdAt, &updatedAt); err != nil {
			return nil, err
		}
		a.Platform = platform.String
		a.Note = note.String
		a.StartDate = startDate.String
		if shares.Valid {
			v := shares.Float64
			a.Shares = &v
		}
		if manualValue.Valid {
			v := manualValue.Float64
			a.ManualValue = &v
		}
		if annualYield.Valid {
			v := annualYield.Float64
			a.AnnualYieldRate = &v
		}
		if purchaseFee.Valid {
			v := purchaseFee.Float64
			a.PurchaseFeeRate = &v
		}
		a.Closed = closed != 0
		if closedRealized.Valid {
			v := closedRealized.Float64
			a.ClosedRealized = &v
		}
		a.ClosedDate = closedDate.String
		a.CreatedAt = createdAt
		a.UpdatedAt = updatedAt
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *ExternalRepo) CreateAsset(ctx context.Context, a *external.Asset) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO external_assets (asset_type, code, name, platform, cost_amount, shares, manual_value, note)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.AssetType, a.Code, a.Name, a.Platform, a.CostAmount, a.Shares, a.ManualValue, a.Note)
	if err != nil {
		return 0, fmt.Errorf("sqlite: create ext asset: %w", err)
	}
	return res.LastInsertId()
}

func (r *ExternalRepo) UpdateAsset(ctx context.Context, a *external.Asset) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE external_assets SET cost_amount = ?, shares = ?, pending_amount = ?, updated_at = datetime('now')
		 WHERE id = ?`, a.CostAmount, a.Shares, a.PendingAmount, a.ID)
	return err
}

func (r *ExternalRepo) DeleteAsset(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM external_assets WHERE id = ?`, id)
	return err
}

func (r *ExternalRepo) ListActions(ctx context.Context, assetID int64) ([]external.Action, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, asset_id, action_type, amount, shares, unit_price, fee,
		        trade_date, trade_time, status, note, interest_part, created_at
		 FROM external_asset_actions WHERE asset_id = ? ORDER BY trade_date, id`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]external.Action, 0)
	for rows.Next() {
		var a external.Action
		var shares, unitPrice, interestPart sql.NullFloat64
		var tradeTime, note sql.NullString
		if err := rows.Scan(&a.ID, &a.AssetID, &a.ActionType, &a.Amount, &shares,
			&unitPrice, &a.Fee, &a.TradeDate, &tradeTime, &a.Status, &note,
			&interestPart, &a.CreatedAt); err != nil {
			return nil, err
		}
		if shares.Valid {
			v := shares.Float64
			a.Shares = &v
		}
		if unitPrice.Valid {
			v := unitPrice.Float64
			a.UnitPrice = &v
		}
		if interestPart.Valid {
			v := interestPart.Float64
			a.InterestPart = &v
		}
		a.TradeTime = tradeTime.String
		a.Note = note.String
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *ExternalRepo) CreateAction(ctx context.Context, a *external.Action) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO external_asset_actions (asset_id, action_type, amount, shares, unit_price, fee, trade_date, trade_time, status, note)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.AssetID, a.ActionType, a.Amount, a.Shares, a.UnitPrice, a.Fee,
		a.TradeDate, a.TradeTime, a.Status, a.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *ExternalRepo) UpdateAction(ctx context.Context, a *external.Action) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE external_asset_actions SET amount = ?, shares = ?, status = ? WHERE id = ?`,
		a.Amount, a.Shares, a.Status, a.ID)
	return err
}

func (r *ExternalRepo) DeleteAction(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM external_asset_actions WHERE id = ?`, id)
	return err
}

func (r *ExternalRepo) ConfirmAction(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE external_asset_actions SET status = 'confirmed' WHERE id = ? AND status = 'pending'`, id)
	if err != nil {
		return err
	}
	// 检查是否有行被更新
	return nil
}

// ---- DCA ----

func (r *ExternalRepo) ListDCASchedules(ctx context.Context) ([]external.DCASchedule, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, asset_id, mode, value, frequency, day_of_month, day_of_week, status, next_due, last_fired_at, note
		 FROM dca_schedules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]external.DCASchedule, 0)
	for rows.Next() {
		var d external.DCASchedule
		var dom, dow sql.NullInt64
		var nextDue, lastFired, note sql.NullString
		if err := rows.Scan(&d.ID, &d.AssetID, &d.Mode, &d.Value, &d.Frequency,
			&dom, &dow, &d.Status, &nextDue, &lastFired, &note); err != nil {
			return nil, err
		}
		if dom.Valid {
			v := int(dom.Int64)
			d.DayOfMonth = &v
		}
		if dow.Valid {
			v := int(dow.Int64)
			d.DayOfWeek = &v
		}
		d.NextDue = nextDue.String
		d.LastFiredAt = lastFired.String
		d.Note = note.String
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *ExternalRepo) CreateDCASchedule(ctx context.Context, d *external.DCASchedule) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO dca_schedules (asset_id, mode, value, frequency, day_of_month, day_of_week, status, next_due, note)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.AssetID, d.Mode, d.Value, d.Frequency, d.DayOfMonth, d.DayOfWeek, d.Status, d.NextDue, d.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *ExternalRepo) UpdateDCASchedule(ctx context.Context, d *external.DCASchedule) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE dca_schedules SET status = ?, next_due = ?, last_fired_at = ? WHERE id = ?`,
		d.Status, d.NextDue, d.LastFiredAt, d.ID)
	return err
}

func (r *ExternalRepo) DeleteDCASchedule(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM dca_schedules WHERE id = ?`, id)
	return err
}

// mapErr 供 external 包复用
func init() {
	_ = errors.New // 确保 errors 包被引用
}
