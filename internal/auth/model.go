// Package auth 实现账号体系：注册、登录、会话与邮箱验证码。
//
// 本包是业务域（内层），只依赖标准库与 golang.org/x/crypto。
// 它不知道 gin、不知道 SQLite——数据访问经由本包自己定义的 Repository 接口，
// 由 internal/infra/sqlite 提供实现。这就是依赖倒置：接口属于使用方，
// 实现属于外层，因此依赖箭头始终从外指向内。
package auth

import (
	"context"
	"time"
)

// User 是一个账号。PasswordHash 存的是 scrypt 派生结果，绝不出现明文。
type User struct {
	ID           int64
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

// Session 是一次登录产生的会话。
//
// 注意这里存的是 TokenHash = sha256(明文 token)，明文只在下发 cookie 的那一刻
// 存在于内存中。数据库泄露不会直接导致会话被劫持。
type Session struct {
	TokenHash string
	UserID    int64
	ExpiresAt time.Time
	CreatedAt time.Time
}

// Expired 判断会话在给定时刻是否已过期。
func (s *Session) Expired(now time.Time) bool {
	return !now.Before(s.ExpiresAt)
}

// EmailCode 是一次性邮箱验证码。每个邮箱同时只保留最新一条。
type EmailCode struct {
	Email     string
	Code      string
	ExpiresAt time.Time
	Attempts  int
	SentAt    time.Time
}

// Repository 抽象 auth 所需的全部持久化操作。
//
// 约定：查询不到目标时返回 ErrNotFound（而不是 sql.ErrNoRows），
// 这样业务层无需知道底层是 SQL 还是别的什么。
type Repository interface {
	CreateUser(ctx context.Context, email, passwordHash string) (*User, error)
	UserByEmail(ctx context.Context, email string) (*User, error)
	UserByID(ctx context.Context, id int64) (*User, error)
	CountUsers(ctx context.Context) (int64, error)

	CreateSession(ctx context.Context, s *Session) error
	SessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error)

	UpsertEmailCode(ctx context.Context, c *EmailCode) error
	EmailCodeByEmail(ctx context.Context, email string) (*EmailCode, error)
	BumpEmailCodeAttempts(ctx context.Context, email string) error
	DeleteEmailCode(ctx context.Context, email string) error
}

// Mailer 负责把验证码送达用户。
// 开发环境用 LogMailer 打日志即可，生产接 SMTP —— 业务层对此无感。
type Mailer interface {
	SendCode(ctx context.Context, email, code string) error
}

// Provisioner 在新用户注册成功后为其准备数据空间（用户库）。
// 由 internal/store.Manager 实现；业务层只表达意图，不关心是文件还是 schema。
type Provisioner interface {
	ProvisionUser(ctx context.Context, userID int64) error
}
