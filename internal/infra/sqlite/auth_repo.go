// Package sqlite 提供各业务域 Repository 接口的 SQLite 实现。
//
// 这里是「外层」：它 import 业务域（internal/auth）以实现其接口，
// 而业务域永远不会 import 本包。依赖箭头由外指向内，符合简洁架构。
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jwcen/argent-go/internal/auth"
)

// timeLayout 是数据库中时间列的存储格式。
//
// 必须与 Python 原版一致：实测旧库 auth_sessions.expires_at 形如
// '2026-08-25 09:35:19'（空格分隔、UTC、无时区后缀），而非 RFC3339。
// 写入用它，读取时兼容带 'T' 的变体，保证两套系统能读写同一份数据。
const timeLayout = "2006-01-02 15:04:05"

// AuthRepo 用全局库实现 auth.Repository。
type AuthRepo struct {
	db *sql.DB
}

// NewAuthRepo 构造仓储。db 必须是全局库句柄。
func NewAuthRepo(db *sql.DB) *AuthRepo { return &AuthRepo{db: db} }

// 编译期断言：确保 *AuthRepo 完整实现了 auth.Repository。
// 这行代码不产生运行时开销，但接口少实现一个方法就会编译失败——
// 比等到运行时才发现要好得多。
var _ auth.Repository = (*AuthRepo)(nil)

// ---------- users ----------

func (r *AuthRepo) CreateUser(ctx context.Context, email, passwordHash string) (*auth.User, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO users (email, password_hash) VALUES (?, ?)`, email, passwordHash)
	if err != nil {
		// UNIQUE 冲突翻译成领域错误，业务层无需认识 SQLite 的错误文本。
		if isUniqueViolation(err) {
			return nil, auth.ErrEmailTaken
		}
		return nil, fmt.Errorf("sqlite: insert user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("sqlite: last insert id: %w", err)
	}
	return r.UserByID(ctx, id)
}

func (r *AuthRepo) UserByEmail(ctx context.Context, email string) (*auth.User, error) {
	return r.scanUser(r.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, created_at FROM users WHERE email = ?`, email))
}

func (r *AuthRepo) UserByID(ctx context.Context, id int64) (*auth.User, error) {
	return r.scanUser(r.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, created_at FROM users WHERE id = ?`, id))
}

func (r *AuthRepo) CountUsers(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite: count users: %w", err)
	}
	return n, nil
}

func (r *AuthRepo) scanUser(row *sql.Row) (*auth.User, error) {
	var (
		u         auth.User
		createdAt sql.NullString
	)
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, auth.ErrNotFound
		}
		return nil, fmt.Errorf("sqlite: scan user: %w", err)
	}
	u.CreatedAt = parseTime(createdAt.String)
	return &u, nil
}

// ---------- sessions ----------

func (r *AuthRepo) CreateSession(ctx context.Context, s *auth.Session) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO auth_sessions (token_hash, user_id, expires_at, created_at)
		 VALUES (?, ?, ?, ?)`,
		s.TokenHash, s.UserID, formatTime(s.ExpiresAt), formatTime(s.CreatedAt))
	if err != nil {
		return fmt.Errorf("sqlite: insert session: %w", err)
	}
	return nil
}

func (r *AuthRepo) SessionByTokenHash(ctx context.Context, tokenHash string) (*auth.Session, error) {
	var (
		s                    auth.Session
		expiresAt, createdAt sql.NullString
	)
	err := r.db.QueryRowContext(ctx,
		`SELECT token_hash, user_id, expires_at, created_at FROM auth_sessions WHERE token_hash = ?`,
		tokenHash,
	).Scan(&s.TokenHash, &s.UserID, &expiresAt, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, auth.ErrNotFound
		}
		return nil, fmt.Errorf("sqlite: scan session: %w", err)
	}
	s.ExpiresAt = parseTime(expiresAt.String)
	s.CreatedAt = parseTime(createdAt.String)
	return &s, nil
}

func (r *AuthRepo) DeleteSession(ctx context.Context, tokenHash string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM auth_sessions WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("sqlite: delete session: %w", err)
	}
	return nil
}

func (r *AuthRepo) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM auth_sessions WHERE expires_at <= ?`, formatTime(now))
	if err != nil {
		return 0, fmt.Errorf("sqlite: purge sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: rows affected: %w", err)
	}
	return n, nil
}

// ---------- email codes ----------

func (r *AuthRepo) UpsertEmailCode(ctx context.Context, c *auth.EmailCode) error {
	// email 是主键，ON CONFLICT 覆盖旧码并重置 attempts。
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO email_codes (email, code, expires_at, attempts, sent_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(email) DO UPDATE SET
		     code = excluded.code,
		     expires_at = excluded.expires_at,
		     attempts = 0,
		     sent_at = excluded.sent_at`,
		c.Email, c.Code, formatTime(c.ExpiresAt), c.Attempts, formatTime(c.SentAt))
	if err != nil {
		return fmt.Errorf("sqlite: upsert email code: %w", err)
	}
	return nil
}

func (r *AuthRepo) EmailCodeByEmail(ctx context.Context, email string) (*auth.EmailCode, error) {
	var (
		c                 auth.EmailCode
		expiresAt, sentAt sql.NullString
	)
	err := r.db.QueryRowContext(ctx,
		`SELECT email, code, expires_at, attempts, sent_at FROM email_codes WHERE email = ?`,
		email,
	).Scan(&c.Email, &c.Code, &expiresAt, &c.Attempts, &sentAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, auth.ErrNotFound
		}
		return nil, fmt.Errorf("sqlite: scan email code: %w", err)
	}
	c.ExpiresAt = parseTime(expiresAt.String)
	c.SentAt = parseTime(sentAt.String)
	return &c, nil
}

func (r *AuthRepo) BumpEmailCodeAttempts(ctx context.Context, email string) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE email_codes SET attempts = attempts + 1 WHERE email = ?`, email); err != nil {
		return fmt.Errorf("sqlite: bump attempts: %w", err)
	}
	return nil
}

func (r *AuthRepo) DeleteEmailCode(ctx context.Context, email string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM email_codes WHERE email = ?`, email); err != nil {
		return fmt.Errorf("sqlite: delete email code: %w", err)
	}
	return nil
}

// ---------- helpers ----------

func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

// parseTime 容忍多种历史格式：空格分隔、带 'T'、带小数秒。
// 解析失败返回零值 time.Time —— 上层用 Expired() 判断时零值必然算过期，
// 属于安全的失败方向。
func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		timeLayout,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05.999999",
		"2006-01-02T15:04:05.999999",
		time.RFC3339,
	}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, s, time.UTC); err == nil {
			return t
		}
	}
	return time.Time{}
}

// isUniqueViolation 判断错误是否为唯一约束冲突。
// modernc.org/sqlite 未暴露稳定的错误码常量，这里按消息匹配。
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint failed") ||
		strings.Contains(msg, "constraint failed: unique")
}
