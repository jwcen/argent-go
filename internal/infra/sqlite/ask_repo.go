package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/jwcen/argent-go/internal/agent"
)

// AskRepo 实现 agent.SessionStore，把问问市场的会话/消息存进用户库。
type AskRepo struct{ db *sql.DB }

func NewAskRepo(db *sql.DB) *AskRepo { return &AskRepo{db: db} }

var _ agent.SessionStore = (*AskRepo)(nil)

func (r *AskRepo) CreateSession(ctx context.Context, userID int64, title string) (*agent.Session, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO ask_sessions (user_id, title, updated_at) VALUES (?, ?, datetime('now'))`,
		userID, title)
	if err != nil {
		return nil, fmt.Errorf("sqlite: create ask session: %w", err)
	}
	id, _ := res.LastInsertId()
	return &agent.Session{ID: id, Title: title}, nil
}

func (r *AskRepo) ListSessions(ctx context.Context, userID int64) ([]agent.Session, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT s.id, s.title, s.updated_at, COUNT(m.id)
		 FROM ask_sessions s LEFT JOIN ask_messages m ON m.session_id = s.id
		 WHERE s.user_id = ? GROUP BY s.id ORDER BY s.updated_at DESC, s.id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list ask sessions: %w", err)
	}
	defer rows.Close()

	var out []agent.Session
	for rows.Next() {
		var s agent.Session
		var updated sql.NullString
		if err := rows.Scan(&s.ID, &s.Title, &updated, &s.MsgCount); err != nil {
			return nil, err
		}
		s.UpdatedAt = updated.String
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *AskRepo) GetSession(ctx context.Context, userID, id int64) (*agent.Session, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, title, updated_at FROM ask_sessions WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get ask session: %w", err)
	}
	defer rows.Close()

	var s agent.Session
	if !rows.Next() {
		return nil, agent.ErrSessionNotFound
	}
	var updated sql.NullString
	if err := rows.Scan(&s.ID, &s.Title, &updated); err != nil {
		return nil, err
	}
	s.UpdatedAt = updated.String
	return &s, nil
}

func (r *AskRepo) DeleteSession(ctx context.Context, userID, id int64) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM ask_sessions WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("sqlite: delete ask session: %w", err)
	}
	r.db.ExecContext(ctx, `DELETE FROM ask_messages WHERE session_id = ?`, id)
	aff, _ := res.RowsAffected()
	if aff == 0 {
		return agent.ErrSessionNotFound
	}
	return nil
}

func (r *AskRepo) AppendMessage(ctx context.Context, userID, sessionID int64, role, content string, meta agent.MessageMeta) (*agent.Message, error) {
	// 校验会话归属，避免越权写入他人会话。
	var owner int64
	if err := r.db.QueryRowContext(ctx,
		`SELECT user_id FROM ask_sessions WHERE id = ?`, sessionID).Scan(&owner); err != nil {
		if err == sql.ErrNoRows {
			return nil, agent.ErrSessionNotFound
		}
		return nil, fmt.Errorf("sqlite: check ask session owner: %w", err)
	}
	if owner != userID {
		return nil, agent.ErrSessionNotFound
	}

	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("sqlite: marshal ask meta: %w", err)
	}

	res, err := r.db.ExecContext(ctx,
		`INSERT INTO ask_messages (session_id, user_id, role, content, meta, created_at)
		 VALUES (?, ?, ?, ?, ?, datetime('now'))`,
		sessionID, userID, role, content, string(metaJSON))
	if err != nil {
		return nil, fmt.Errorf("sqlite: append ask message: %w", err)
	}
	id, _ := res.LastInsertId()
	// 触碰会话的 updated_at，让会话列表按最近活跃排序。
	r.db.ExecContext(ctx, `UPDATE ask_sessions SET updated_at = datetime('now') WHERE id = ?`, sessionID)

	return &agent.Message{
		ID:        id,
		SessionID: sessionID,
		Role:      role,
		Content:   content,
		Meta:      meta,
	}, nil
}

func (r *AskRepo) ListMessages(ctx context.Context, sessionID int64) ([]agent.Message, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, role, content, meta, created_at FROM ask_messages WHERE session_id = ? ORDER BY id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list ask messages: %w", err)
	}
	defer rows.Close()

	var out []agent.Message
	for rows.Next() {
		var m agent.Message
		var metaJSON, created sql.NullString
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &metaJSON, &created); err != nil {
			return nil, err
		}
		if metaJSON.Valid && metaJSON.String != "" {
			_ = json.Unmarshal([]byte(metaJSON.String), &m.Meta)
		}
		m.SessionID = sessionID
		m.CreatedAt = created.String
		out = append(out, m)
	}
	return out, rows.Err()
}
