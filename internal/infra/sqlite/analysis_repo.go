package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jwcen/argent-go/internal/strategy"
)

// AnalysisRepo 实现 strategy.AnalysisStore。
type AnalysisRepo struct {
	db *sql.DB
}

func NewAnalysisRepo(db *sql.DB) *AnalysisRepo {
	return &AnalysisRepo{db: db}
}

func (r *AnalysisRepo) SaveAnalysis(ctx context.Context, a *strategy.Analysis) (int64, error) {
	// 同股同日重新分析：先删后插（事务保证原子性）。
	// 用 created_at 的日期前缀（YYYY-MM-DD）作为「同一天」的判定。
	now := time.Now()
	today := now.Format("2006-01-02")
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("sqlite: save analysis begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM ai_analyses WHERE code = ? AND substr(created_at, 1, 10) = ?`,
		a.Code, today); err != nil {
		return 0, fmt.Errorf("sqlite: delete stale analysis: %w", err)
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO ai_analyses (code, name, direction, advice, trigger, risk, price_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Code, a.Name, a.Direction, a.Advice, a.Trigger, a.Risk, a.PriceAt,
		now.Format("2006-01-02 15:04:05"))
	if err != nil {
		return 0, fmt.Errorf("sqlite: save analysis: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("sqlite: save analysis commit: %w", err)
	}
	return res.LastInsertId()
}

func (r *AnalysisRepo) ListAnalyses(ctx context.Context, code string) ([]strategy.Analysis, error) {
	query := `SELECT id, code, name, direction, advice, trigger, risk, price_at, created_at
	          FROM ai_analyses`
	args := []any{}
	if code != "" {
		query += ` WHERE code = ?`
		args = append(args, code)
	}
	query += ` ORDER BY id DESC`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list analyses: %w", err)
	}
	defer rows.Close()

	out := make([]strategy.Analysis, 0)
	for rows.Next() {
		var a strategy.Analysis
		var priceAt sql.NullFloat64
		if err := rows.Scan(&a.ID, &a.Code, &a.Name, &a.Direction, &a.Advice, &a.Trigger, &a.Risk, &priceAt, &a.CreatedAt); err != nil {
			return nil, err
		}
		if priceAt.Valid {
			a.PriceAt = priceAt.Float64
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
