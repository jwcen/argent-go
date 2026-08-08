package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jwcen/argent-go/internal/portfolio"
)

// PortfolioRepo 用用户库实现 portfolio.Repository。
type PortfolioRepo struct {
	db *sql.DB
}

func NewPortfolioRepo(db *sql.DB) *PortfolioRepo { return &PortfolioRepo{db: db} }

var _ portfolio.Repository = (*PortfolioRepo)(nil)

// ---------- Holdings ----------

func (r *PortfolioRepo) ListHoldings(ctx context.Context) ([]portfolio.Holding, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, stock_code, stock_name, shares, cost_price, purchase_date, broker,
		        created_at, updated_at
		 FROM holdings ORDER BY stock_code`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list holdings: %w", err)
	}
	defer rows.Close()

	out := make([]portfolio.Holding, 0)
	for rows.Next() {
		var h portfolio.Holding
		var purchaseDate sql.NullString
		var broker sql.NullString
		var createdAt, updatedAt string
		if err := rows.Scan(&h.ID, &h.StockCode, &h.StockName, &h.Shares, &h.CostPrice,
			&purchaseDate, &broker, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		h.PurchaseDate = purchaseDate.String
		h.Broker = broker.String
		h.CreatedAt = createdAt
		h.UpdatedAt = updatedAt
		out = append(out, h)
	}
	return out, rows.Err()
}

func (r *PortfolioRepo) GetHolding(ctx context.Context, code string) (*portfolio.Holding, error) {
	var h portfolio.Holding
	var purchaseDate, broker sql.NullString
	var createdAt, updatedAt string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, stock_code, stock_name, shares, cost_price, purchase_date, broker,
		        created_at, updated_at
		 FROM holdings WHERE stock_code = ?`, code).Scan(
		&h.ID, &h.StockCode, &h.StockName, &h.Shares, &h.CostPrice,
		&purchaseDate, &broker, &createdAt, &updatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	h.PurchaseDate = purchaseDate.String
	h.Broker = broker.String
	h.CreatedAt = createdAt
	h.UpdatedAt = updatedAt
	return &h, nil
}

func (r *PortfolioRepo) UpsertHolding(ctx context.Context, h *portfolio.Holding) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO holdings (stock_code, stock_name, shares, cost_price, purchase_date, broker, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(stock_code) DO UPDATE SET
		   stock_name = excluded.stock_name,
		   shares = excluded.shares,
		   cost_price = excluded.cost_price,
		   purchase_date = excluded.purchase_date,
		   broker = excluded.broker,
		   updated_at = ?`,
		h.StockCode, h.StockName, h.Shares, h.CostPrice, h.PurchaseDate, h.Broker,
		formatTime(time.Now()), formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("sqlite: upsert holding: %w", err)
	}
	return nil
}

func (r *PortfolioRepo) DeleteHolding(ctx context.Context, code string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM holdings WHERE stock_code = ?`, code)
	if err != nil {
		return fmt.Errorf("sqlite: delete holding: %w", err)
	}
	return nil
}

// ---------- Actions ----------

func (r *PortfolioRepo) ListActions(ctx context.Context, code string) ([]portfolio.Action, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, stock_code, action_type, price, shares, tranche_id, note,
		        trade_date, trade_time, fee, broker, created_at
		 FROM position_actions WHERE stock_code = ? ORDER BY trade_date, id`, code)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list actions: %w", err)
	}
	defer rows.Close()
	return scanActions(rows)
}

func (r *PortfolioRepo) ListAllActions(ctx context.Context) ([]portfolio.Action, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, stock_code, action_type, price, shares, tranche_id, note,
		        trade_date, trade_time, fee, broker, created_at
		 FROM position_actions ORDER BY stock_code, trade_date, id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list all actions: %w", err)
	}
	defer rows.Close()
	return scanActions(rows)
}

// GetAction 走主键索引取单条，避免为了定位一行而把整张流水表读进内存。
// 不存在时返回 (nil, nil)，由调用方决定是 404 还是其他语义。
func (r *PortfolioRepo) GetAction(ctx context.Context, id int64) (*portfolio.Action, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, stock_code, action_type, price, shares, tranche_id, note,
		        trade_date, trade_time, fee, broker, created_at
		 FROM position_actions WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get action: %w", err)
	}
	defer rows.Close()
	out, err := scanActions(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

func (r *PortfolioRepo) CreateAction(ctx context.Context, a *portfolio.Action) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO position_actions (stock_code, action_type, price, shares, tranche_id,
		   note, trade_date, trade_time, fee, broker)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.StockCode, a.ActionType, a.Price, a.Shares, a.TrancheID,
		a.Note, a.TradeDate, a.TradeTime, a.Fee, a.Broker)
	if err != nil {
		return 0, fmt.Errorf("sqlite: insert action: %w", err)
	}
	return res.LastInsertId()
}

func (r *PortfolioRepo) UpdateAction(ctx context.Context, a *portfolio.Action) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE position_actions SET
		   stock_code = ?, action_type = ?, price = ?, shares = ?, tranche_id = ?,
		   note = ?, trade_date = ?, trade_time = ?, fee = ?, broker = ?
		 WHERE id = ?`,
		a.StockCode, a.ActionType, a.Price, a.Shares, a.TrancheID,
		a.Note, a.TradeDate, a.TradeTime, a.Fee, a.Broker, a.ID)
	if err != nil {
		return fmt.Errorf("sqlite: update action: %w", err)
	}
	return nil
}

func (r *PortfolioRepo) DeleteAction(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM position_actions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("sqlite: delete action: %w", err)
	}
	return nil
}

// ---------- Brokers ----------

func (r *PortfolioRepo) ListBrokers(ctx context.Context) ([]portfolio.Broker, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, stock_rate, stock_min, etf_rate, etf_min, is_default FROM brokers ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list brokers: %w", err)
	}
	defer rows.Close()

	out := make([]portfolio.Broker, 0)
	for rows.Next() {
		var b portfolio.Broker
		var isDefault int
		if err := rows.Scan(&b.ID, &b.Name, &b.StockRate, &b.StockMin,
			&b.EtfRate, &b.EtfMin, &isDefault); err != nil {
			return nil, err
		}
		b.IsDefault = isDefault != 0
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *PortfolioRepo) GetDefaultBroker(ctx context.Context) (*portfolio.Broker, error) {
	var b portfolio.Broker
	var isDefault int
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, stock_rate, stock_min, etf_rate, etf_min, is_default
		 FROM brokers WHERE is_default = 1 LIMIT 1`).Scan(
		&b.ID, &b.Name, &b.StockRate, &b.StockMin, &b.EtfRate, &b.EtfMin, &isDefault)
	if err != nil {
		return nil, mapErr(err)
	}
	b.IsDefault = isDefault != 0
	return &b, nil
}

func (r *PortfolioRepo) CreateBroker(ctx context.Context, b *portfolio.Broker) (int64, error) {
	isDefault := 0
	if b.IsDefault {
		isDefault = 1
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO brokers (name, stock_rate, stock_min, etf_rate, etf_min, is_default)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		b.Name, b.StockRate, b.StockMin, b.EtfRate, b.EtfMin, isDefault)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, portfolio.ErrDuplicateBroker
		}
		return 0, fmt.Errorf("sqlite: insert broker: %w", err)
	}
	return res.LastInsertId()
}

func (r *PortfolioRepo) UpdateBroker(ctx context.Context, b *portfolio.Broker) error {
	isDefault := 0
	if b.IsDefault {
		isDefault = 1
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE brokers SET name = ?, stock_rate = ?, stock_min = ?, etf_rate = ?, etf_min = ?, is_default = ?
		 WHERE id = ?`,
		b.Name, b.StockRate, b.StockMin, b.EtfRate, b.EtfMin, isDefault, b.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return portfolio.ErrDuplicateBroker
		}
		return fmt.Errorf("sqlite: update broker: %w", err)
	}
	return nil
}

func (r *PortfolioRepo) DeleteBroker(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM brokers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("sqlite: delete broker: %w", err)
	}
	return nil
}

// ---------- Thesis ----------

func (r *PortfolioRepo) GetThesis(ctx context.Context, code string) (*portfolio.Thesis, error) {
	var t portfolio.Thesis
	var name sql.NullString
	var createdAt, updatedAt string
	err := r.db.QueryRowContext(ctx,
		`SELECT code, name, thesis, created_at, updated_at FROM position_thesis WHERE code = ?`, code).
		Scan(&t.Code, &name, &t.Thesis, &createdAt, &updatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	t.Name = name.String
	t.CreatedAt = createdAt
	t.UpdatedAt = updatedAt
	return &t, nil
}

func (r *PortfolioRepo) UpsertThesis(ctx context.Context, t *portfolio.Thesis) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO position_thesis (code, name, thesis, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(code) DO UPDATE SET name = excluded.name, thesis = excluded.thesis, updated_at = ?`,
		t.Code, t.Name, t.Thesis, formatTime(time.Now()), formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("sqlite: upsert thesis: %w", err)
	}
	return nil
}

func (r *PortfolioRepo) DeleteThesis(ctx context.Context, code string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM position_thesis WHERE code = ?`, code)
	if err != nil {
		return fmt.Errorf("sqlite: delete thesis: %w", err)
	}
	return nil
}

// ---------- Watchlist ----------

func (r *PortfolioRepo) ListWatchlist(ctx context.Context) ([]portfolio.WatchlistItem, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT stock_code, stock_name, added_at, added_price FROM watchlist ORDER BY added_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list watchlist: %w", err)
	}
	defer rows.Close()

	out := make([]portfolio.WatchlistItem, 0)
	for rows.Next() {
		var w portfolio.WatchlistItem
		var name sql.NullString
		var addedAt sql.NullString
		var addedPrice sql.NullFloat64
		if err := rows.Scan(&w.StockCode, &name, &addedAt, &addedPrice); err != nil {
			return nil, err
		}
		w.StockName = name.String
		w.AddedAt = addedAt.String
		if addedPrice.Valid {
			v := addedPrice.Float64
			w.AddedPrice = &v
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (r *PortfolioRepo) AddWatchlist(ctx context.Context, w *portfolio.WatchlistItem) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO watchlist (stock_code, stock_name, added_at, added_price)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(stock_code) DO UPDATE SET stock_name = excluded.stock_name`,
		w.StockCode, w.StockName, w.AddedAt, w.AddedPrice)
	if err != nil {
		return fmt.Errorf("sqlite: add watchlist: %w", err)
	}
	return nil
}

func (r *PortfolioRepo) RemoveWatchlist(ctx context.Context, code string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM watchlist WHERE stock_code = ?`, code)
	if err != nil {
		return fmt.Errorf("sqlite: remove watchlist: %w", err)
	}
	return nil
}

// ---------- helpers ----------

// mapErr 把 sql.ErrNoRows 翻译成 portfolio.ErrNotFound，
// 这样业务层不需要接触 database/sql 的错误类型。
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return portfolio.ErrNotFound
	}
	return fmt.Errorf("sqlite: %w", err)
}

func scanActions(rows *sql.Rows) ([]portfolio.Action, error) {
	out := make([]portfolio.Action, 0)
	for rows.Next() {
		var a portfolio.Action
		var trancheID sql.NullInt64
		var note sql.NullString
		var tradeDate, tradeTime, broker sql.NullString
		var fee sql.NullFloat64
		var createdAt string
		if err := rows.Scan(&a.ID, &a.StockCode, &a.ActionType, &a.Price, &a.Shares,
			&trancheID, &note, &tradeDate, &tradeTime, &fee, &broker, &createdAt); err != nil {
			return nil, err
		}
		if trancheID.Valid {
			v := trancheID.Int64
			a.TrancheID = &v
		}
		a.Note = note.String
		a.TradeDate = tradeDate.String
		a.TradeTime = tradeTime.String
		if fee.Valid {
			v := fee.Float64
			a.Fee = &v
		}
		a.Broker = broker.String
		a.CreatedAt = createdAt
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- Dividend events ----

const dividendCols = `id, stock_code, ex_date, cash_per_share, bonus_ratio, source, note, created_at`

func (r *PortfolioRepo) ListDividendEvents(ctx context.Context, code string) ([]portfolio.DividendEvent, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+dividendCols+` FROM dividend_events WHERE stock_code = ? ORDER BY ex_date DESC`, code)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list dividend events: %w", err)
	}
	defer rows.Close()
	return scanDividendEvents(rows)
}

func (r *PortfolioRepo) ListAllDividendEvents(ctx context.Context) ([]portfolio.DividendEvent, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+dividendCols+` FROM dividend_events ORDER BY stock_code, ex_date`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list all dividend events: %w", err)
	}
	defer rows.Close()
	return scanDividendEvents(rows)
}

// UpsertDividendEvent 按 (stock_code, ex_date) 唯一键幂等写入。
// 用 ON CONFLICT 而不是先查后写：并发导入时先查后写会漏判，
// 而重复插入一条除权事件会让成本被摊薄两次——这类错账很难被发现。
func (r *PortfolioRepo) UpsertDividendEvent(ctx context.Context, e *portfolio.DividendEvent) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO dividend_events (stock_code, ex_date, cash_per_share, bonus_ratio, source, note, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(stock_code, ex_date) DO UPDATE SET
		     cash_per_share = excluded.cash_per_share,
		     bonus_ratio    = excluded.bonus_ratio,
		     source         = excluded.source,
		     note           = excluded.note`,
		e.StockCode, e.ExDate, e.CashPerShare, e.BonusRatio, e.Source, e.Note, formatTime(time.Now()))
	if err != nil {
		return 0, fmt.Errorf("sqlite: upsert dividend event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if id == 0 {
		// ON CONFLICT 走了 UPDATE 分支，LastInsertId 不可靠，回查一次
		_ = r.db.QueryRowContext(ctx,
			`SELECT id FROM dividend_events WHERE stock_code = ? AND ex_date = ?`,
			e.StockCode, e.ExDate).Scan(&id)
	}
	e.ID = id
	return id, nil
}

func (r *PortfolioRepo) DeleteDividendEvent(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM dividend_events WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("sqlite: delete dividend event: %w", err)
	}
	return nil
}

func scanDividendEvents(rows *sql.Rows) ([]portfolio.DividendEvent, error) {
	out := make([]portfolio.DividendEvent, 0)
	for rows.Next() {
		var e portfolio.DividendEvent
		var source, note sql.NullString
		var createdAt sql.NullString
		if err := rows.Scan(&e.ID, &e.StockCode, &e.ExDate, &e.CashPerShare,
			&e.BonusRatio, &source, &note, &createdAt); err != nil {
			return nil, err
		}
		e.Source = source.String
		e.Note = note.String
		e.CreatedAt = createdAt.String
		out = append(out, e)
	}
	return out, rows.Err()
}
