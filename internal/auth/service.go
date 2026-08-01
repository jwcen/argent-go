package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/mail"
	"strings"
	"time"
)

// Config 是 auth 的可调参数。零值字段由 DefaultConfig 补齐。
type Config struct {
	CodeTTL      time.Duration // 验证码有效期
	CodeCooldown time.Duration // 两次发码最小间隔
	CodeLength   int           // 验证码位数
	MaxAttempts  int           // 单个验证码最大尝试次数
	SessionTTL   time.Duration // 会话有效期
	MinPassword  int           // 密码最短长度
}

// DefaultConfig 返回生产可用的默认值。
// SessionTTL 取 30 天，与原版 Python 实测数据一致（创建到过期正好 30 天），
// 保证两套系统并行运行期间会话语义完全相同。
func DefaultConfig() Config {
	return Config{
		CodeTTL:      10 * time.Minute,
		CodeCooldown: 60 * time.Second,
		CodeLength:   6,
		MaxAttempts:  5,
		SessionTTL:   30 * 24 * time.Hour,
		MinPassword:  8,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.CodeTTL <= 0 {
		c.CodeTTL = d.CodeTTL
	}
	if c.CodeCooldown < 0 {
		c.CodeCooldown = d.CodeCooldown
	}
	if c.CodeLength <= 0 {
		c.CodeLength = d.CodeLength
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = d.MaxAttempts
	}
	if c.SessionTTL <= 0 {
		c.SessionTTL = d.SessionTTL
	}
	if c.MinPassword <= 0 {
		c.MinPassword = d.MinPassword
	}
	return c
}

// Service 承载 auth 的全部业务规则。
//
// now 字段是可注入的时钟：测试里替换成固定时间，就能确定性地验证
// 「冷却 60 秒」「验证码 10 分钟过期」这类与时间强相关的逻辑，
// 而不必真的 sleep。这是 Go 里处理时间依赖的标准手法。
type Service struct {
	repo      Repository
	mailer    Mailer
	provision Provisioner
	cfg       Config
	now       func() time.Time
}

// NewService 构造 Service。provision 可为 nil（例如单测中不关心用户库）。
func NewService(repo Repository, mailer Mailer, provision Provisioner, cfg Config) *Service {
	return &Service{
		repo:      repo,
		mailer:    mailer,
		provision: provision,
		cfg:       cfg.withDefaults(),
		now:       time.Now,
	}
}

// SetClock 替换内部时钟，仅供测试使用。
func (s *Service) SetClock(f func() time.Time) { s.now = f }

// SendCode 生成并投递邮箱验证码，带 60 秒冷却。
func (s *Service) SendCode(ctx context.Context, email string) error {
	email, err := normalizeEmail(email)
	if err != nil {
		return err
	}
	now := s.now().UTC()

	// 冷却检查：存在未超冷却期的旧码则拒绝，防止被当成发信炮台。
	prev, err := s.repo.EmailCodeByEmail(ctx, email)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if prev != nil && now.Sub(prev.SentAt) < s.cfg.CodeCooldown {
		return ErrCodeCooldown
	}

	code, err := randomDigits(s.cfg.CodeLength)
	if err != nil {
		return err
	}

	// 覆盖式写入：同一邮箱只保留最新一条，attempts 归零。
	if err := s.repo.UpsertEmailCode(ctx, &EmailCode{
		Email:     email,
		Code:      code,
		ExpiresAt: now.Add(s.cfg.CodeTTL),
		Attempts:  0,
		SentAt:    now,
	}); err != nil {
		return err
	}

	return s.mailer.SendCode(ctx, email, code)
}

// Register 校验验证码后创建账号，并立即建立会话（注册即登录）。
// 返回新用户与明文 session token（调用方负责写入 cookie）。
func (s *Service) Register(ctx context.Context, email, password, code string) (*User, string, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return nil, "", err
	}
	if len([]rune(password)) < s.cfg.MinPassword {
		return nil, "", ErrWeakPassword
	}

	if err := s.consumeCode(ctx, email, code); err != nil {
		return nil, "", err
	}

	// 邮箱唯一性：先查一次给出友好错误；真正的并发保证仍来自
	// users.email 上的 UNIQUE 约束（仓储层会把冲突翻译成 ErrEmailTaken）。
	if _, err := s.repo.UserByEmail(ctx, email); err == nil {
		return nil, "", ErrEmailTaken
	} else if !errors.Is(err, ErrNotFound) {
		return nil, "", err
	}

	hash, err := HashPassword(password)
	if err != nil {
		return nil, "", err
	}

	user, err := s.repo.CreateUser(ctx, email, hash)
	if err != nil {
		return nil, "", err
	}

	// 验证码已消费成功，删除以防重放。
	_ = s.repo.DeleteEmailCode(ctx, email)

	// 为新用户准备数据库（首个用户会继承旧库）。
	if s.provision != nil {
		if err := s.provision.ProvisionUser(ctx, user.ID); err != nil {
			return nil, "", fmt.Errorf("auth: provision user %d: %w", user.ID, err)
		}
	}

	token, err := s.issueSession(ctx, user.ID)
	if err != nil {
		return nil, "", err
	}
	return user, token, nil
}

// Login 校验密码并签发会话。
func (s *Service) Login(ctx context.Context, email, password string) (*User, string, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		// 邮箱格式错也统一报凭据错误，不泄露任何信息。
		return nil, "", ErrInvalidCredentials
	}

	user, err := s.repo.UserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, "", ErrInvalidCredentials
		}
		return nil, "", err
	}

	ok, err := VerifyPassword(password, user.PasswordHash)
	if err != nil || !ok {
		return nil, "", ErrInvalidCredentials
	}

	token, err := s.issueSession(ctx, user.ID)
	if err != nil {
		return nil, "", err
	}
	return user, token, nil
}

// Logout 销毁 token 对应的会话。token 无效时静默成功（幂等）。
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.repo.DeleteSession(ctx, HashToken(token))
}

// Authenticate 用明文 token 换取当前用户，供中间件调用。
// 会话过期时顺手删除记录，避免过期数据堆积。
func (s *Service) Authenticate(ctx context.Context, token string) (*User, error) {
	if token == "" {
		return nil, ErrUnauthenticated
	}

	sess, err := s.repo.SessionByTokenHash(ctx, HashToken(token))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrUnauthenticated
		}
		return nil, err
	}
	if sess.Expired(s.now().UTC()) {
		_ = s.repo.DeleteSession(ctx, sess.TokenHash)
		return nil, ErrUnauthenticated
	}

	user, err := s.repo.UserByID(ctx, sess.UserID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrUnauthenticated
		}
		return nil, err
	}
	return user, nil
}

// PurgeExpiredSessions 清理过期会话，供定时任务调用。
func (s *Service) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	return s.repo.DeleteExpiredSessions(ctx, s.now().UTC())
}

// SessionTTL 暴露会话有效期，transport 层据此设置 cookie 的 Max-Age。
func (s *Service) SessionTTL() time.Duration { return s.cfg.SessionTTL }

// consumeCode 校验验证码：存在性、过期、尝试次数、值比对。
func (s *Service) consumeCode(ctx context.Context, email, code string) error {
	rec, err := s.repo.EmailCodeByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrCodeInvalid
		}
		return err
	}

	if rec.Attempts >= s.cfg.MaxAttempts {
		return ErrCodeAttemptsExceeded
	}
	if !s.now().UTC().Before(rec.ExpiresAt) {
		return ErrCodeInvalid
	}

	// 常数时间比较，杜绝按位试探。
	if subtle.ConstantTimeCompare([]byte(rec.Code), []byte(strings.TrimSpace(code))) != 1 {
		// 失败即累加尝试次数，达到上限后该码作废。
		_ = s.repo.BumpEmailCodeAttempts(ctx, email)
		return ErrCodeInvalid
	}
	return nil
}

// issueSession 生成随机 token，落库存其哈希，返回明文 token。
func (s *Service) issueSession(ctx context.Context, userID int64) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	now := s.now().UTC()
	if err := s.repo.CreateSession(ctx, &Session{
		TokenHash: HashToken(token),
		UserID:    userID,
		ExpiresAt: now.Add(s.cfg.SessionTTL),
		CreatedAt: now,
	}); err != nil {
		return "", err
	}
	return token, nil
}

// HashToken 计算 session token 的存储形式：sha256 十六进制。
//
// 与 Python 原版完全一致，因此旧系统签发的 cookie 在 Go 侧依然有效，
// 反之亦然——这是灰度切换期间不掉线的关键。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// randomToken 生成 32 字节随机 token（64 位十六进制）。
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generate session token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// randomDigits 生成 n 位十进制验证码，使用密码学安全随机源。
func randomDigits(n int) (string, error) {
	var sb strings.Builder
	sb.Grow(n)
	for i := 0; i < n; i++ {
		d, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", fmt.Errorf("auth: generate code: %w", err)
		}
		sb.WriteByte(byte('0' + d.Int64()))
	}
	return sb.String(), nil
}

// normalizeEmail 校验并规范化邮箱（去空白 + 转小写）。
// 规范化很重要：否则 Alice@X.com 和 alice@x.com 会被当成两个账号。
func normalizeEmail(email string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(email))
	if e == "" {
		return "", ErrInvalidEmail
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e {
		return "", ErrInvalidEmail
	}
	return e, nil
}
