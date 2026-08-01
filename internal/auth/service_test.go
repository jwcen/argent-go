package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ---------- 测试替身（fake）----------
//
// 这里手写一个内存版 Repository，而不是引入 mock 框架。
// Go 社区偏好这种做法：fake 是普通结构体，行为一目了然，
// 不需要学一套 mock DSL，也不会因为「期望调用次数」之类的断言让测试变脆。

type fakeRepo struct {
	users      map[string]*User
	byID       map[int64]*User
	sessions   map[string]*Session
	codes      map[string]*EmailCode
	nextID     int64
	failCreate error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		users:    make(map[string]*User),
		byID:     make(map[int64]*User),
		sessions: make(map[string]*Session),
		codes:    make(map[string]*EmailCode),
		nextID:   1,
	}
}

func (f *fakeRepo) CreateUser(_ context.Context, email, hash string) (*User, error) {
	if f.failCreate != nil {
		return nil, f.failCreate
	}
	if _, ok := f.users[email]; ok {
		return nil, ErrEmailTaken
	}
	u := &User{ID: f.nextID, Email: email, PasswordHash: hash, CreatedAt: time.Now().UTC()}
	f.nextID++
	f.users[email] = u
	f.byID[u.ID] = u
	return u, nil
}

func (f *fakeRepo) UserByEmail(_ context.Context, email string) (*User, error) {
	if u, ok := f.users[email]; ok {
		return u, nil
	}
	return nil, ErrNotFound
}

func (f *fakeRepo) UserByID(_ context.Context, id int64) (*User, error) {
	if u, ok := f.byID[id]; ok {
		return u, nil
	}
	return nil, ErrNotFound
}

func (f *fakeRepo) CountUsers(context.Context) (int64, error) { return int64(len(f.users)), nil }

func (f *fakeRepo) CreateSession(_ context.Context, s *Session) error {
	f.sessions[s.TokenHash] = s
	return nil
}

func (f *fakeRepo) SessionByTokenHash(_ context.Context, h string) (*Session, error) {
	if s, ok := f.sessions[h]; ok {
		return s, nil
	}
	return nil, ErrNotFound
}

func (f *fakeRepo) DeleteSession(_ context.Context, h string) error {
	delete(f.sessions, h)
	return nil
}

func (f *fakeRepo) DeleteExpiredSessions(_ context.Context, now time.Time) (int64, error) {
	var n int64
	for k, s := range f.sessions {
		if s.Expired(now) {
			delete(f.sessions, k)
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) UpsertEmailCode(_ context.Context, c *EmailCode) error {
	cp := *c
	f.codes[c.Email] = &cp
	return nil
}

func (f *fakeRepo) EmailCodeByEmail(_ context.Context, email string) (*EmailCode, error) {
	if c, ok := f.codes[email]; ok {
		return c, nil
	}
	return nil, ErrNotFound
}

func (f *fakeRepo) BumpEmailCodeAttempts(_ context.Context, email string) error {
	if c, ok := f.codes[email]; ok {
		c.Attempts++
	}
	return nil
}

func (f *fakeRepo) DeleteEmailCode(_ context.Context, email string) error {
	delete(f.codes, email)
	return nil
}

// captureMailer 记录最后一次发出的验证码，方便测试直接取用。
type captureMailer struct {
	lastEmail string
	lastCode  string
	calls     int
}

func (m *captureMailer) SendCode(_ context.Context, email, code string) error {
	m.lastEmail, m.lastCode = email, code
	m.calls++
	return nil
}

// ---------- 测试脚手架 ----------

type harness struct {
	svc    *Service
	repo   *fakeRepo
	mailer *captureMailer
	clock  time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		repo:   newFakeRepo(),
		mailer: &captureMailer{},
		clock:  time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
	}
	h.svc = NewService(h.repo, h.mailer, nil, DefaultConfig())
	h.svc.SetClock(func() time.Time { return h.clock })
	return h
}

// advance 推进可控时钟，用来测试冷却与过期，无需真的 sleep。
func (h *harness) advance(d time.Duration) { h.clock = h.clock.Add(d) }

// registerUser 是常用前置动作：发码 → 用码注册。
func (h *harness) registerUser(t *testing.T, email, pw string) (*User, string) {
	t.Helper()
	ctx := context.Background()
	if err := h.svc.SendCode(ctx, email); err != nil {
		t.Fatalf("SendCode: %v", err)
	}
	u, token, err := h.svc.Register(ctx, email, pw, h.mailer.lastCode)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return u, token
}

// ---------- 测试用例 ----------

func TestSendCode_Cooldown(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if err := h.svc.SendCode(ctx, "a@example.com"); err != nil {
		t.Fatalf("first SendCode: %v", err)
	}

	// 59 秒内重发应被冷却拦截。
	h.advance(59 * time.Second)
	if err := h.svc.SendCode(ctx, "a@example.com"); !errors.Is(err, ErrCodeCooldown) {
		t.Fatalf("expected ErrCodeCooldown, got %v", err)
	}

	// 超过 60 秒后放行。
	h.advance(2 * time.Second)
	if err := h.svc.SendCode(ctx, "a@example.com"); err != nil {
		t.Fatalf("SendCode after cooldown: %v", err)
	}
	if h.mailer.calls != 2 {
		t.Fatalf("mailer called %d times, want 2", h.mailer.calls)
	}
}

func TestSendCode_InvalidEmail(t *testing.T) {
	h := newHarness(t)
	for _, bad := range []string{"", "not-an-email", "a@", "@b.com", "a b@c.com"} {
		if err := h.svc.SendCode(context.Background(), bad); !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("SendCode(%q) = %v, want ErrInvalidEmail", bad, err)
		}
	}
}

func TestRegister_HappyPath(t *testing.T) {
	h := newHarness(t)
	u, token := h.registerUser(t, "new@example.com", "password123")

	if u.ID == 0 || u.Email != "new@example.com" {
		t.Fatalf("unexpected user %+v", u)
	}
	if token == "" {
		t.Fatal("expected non-empty session token")
	}
	// 注册即登录：token 应能换回同一个用户。
	got, err := h.svc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got.ID != u.ID {
		t.Fatalf("Authenticate returned user %d, want %d", got.ID, u.ID)
	}
	// 验证码用完即焚，防重放。
	if _, err := h.repo.EmailCodeByEmail(context.Background(), "new@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatal("email code should be deleted after successful register")
	}
}

func TestRegister_EmailNormalized(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// 大写 + 前后空格应被规范化成同一个账号。
	if err := h.svc.SendCode(ctx, "  Mixed@Example.COM "); err != nil {
		t.Fatalf("SendCode: %v", err)
	}
	u, _, err := h.svc.Register(ctx, "Mixed@Example.com", "password123", h.mailer.lastCode)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if u.Email != "mixed@example.com" {
		t.Fatalf("email = %q, want normalized lowercase", u.Email)
	}
}

func TestRegister_WrongCode(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.svc.SendCode(ctx, "x@example.com"); err != nil {
		t.Fatalf("SendCode: %v", err)
	}
	if _, _, err := h.svc.Register(ctx, "x@example.com", "password123", "000000-wrong"); !errors.Is(err, ErrCodeInvalid) {
		t.Fatalf("expected ErrCodeInvalid, got %v", err)
	}
}

func TestRegister_CodeExpired(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.svc.SendCode(ctx, "x@example.com"); err != nil {
		t.Fatalf("SendCode: %v", err)
	}
	h.advance(11 * time.Minute) // 超过 10 分钟 TTL
	if _, _, err := h.svc.Register(ctx, "x@example.com", "password123", h.mailer.lastCode); !errors.Is(err, ErrCodeInvalid) {
		t.Fatalf("expected ErrCodeInvalid after TTL, got %v", err)
	}
}

func TestRegister_AttemptsExceeded(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.svc.SendCode(ctx, "x@example.com"); err != nil {
		t.Fatalf("SendCode: %v", err)
	}
	// 连续 5 次错码后，第 6 次应报「尝试过多」而非「验证码错误」。
	for i := 0; i < 5; i++ {
		if _, _, err := h.svc.Register(ctx, "x@example.com", "password123", "bad"); !errors.Is(err, ErrCodeInvalid) {
			t.Fatalf("attempt %d: expected ErrCodeInvalid, got %v", i+1, err)
		}
	}
	_, _, err := h.svc.Register(ctx, "x@example.com", "password123", "bad")
	if !errors.Is(err, ErrCodeAttemptsExceeded) {
		t.Fatalf("expected ErrCodeAttemptsExceeded, got %v", err)
	}
	// 即使此时给出正确验证码也必须拒绝。
	if _, _, err := h.svc.Register(ctx, "x@example.com", "password123", h.mailer.lastCode); !errors.Is(err, ErrCodeAttemptsExceeded) {
		t.Fatalf("correct code after lockout should still fail, got %v", err)
	}
}

func TestRegister_WeakPassword(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.svc.SendCode(ctx, "x@example.com"); err != nil {
		t.Fatalf("SendCode: %v", err)
	}
	if _, _, err := h.svc.Register(ctx, "x@example.com", "short", h.mailer.lastCode); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("expected ErrWeakPassword, got %v", err)
	}
}

func TestRegister_DuplicateEmail(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.registerUser(t, "dup@example.com", "password123")

	h.advance(2 * time.Minute) // 越过发码冷却
	if err := h.svc.SendCode(ctx, "dup@example.com"); err != nil {
		t.Fatalf("SendCode: %v", err)
	}
	if _, _, err := h.svc.Register(ctx, "dup@example.com", "password123", h.mailer.lastCode); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("expected ErrEmailTaken, got %v", err)
	}
}

func TestLogin_And_Logout(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	u, _ := h.registerUser(t, "log@example.com", "password123")

	// 正确密码
	got, token, err := h.svc.Login(ctx, "log@example.com", "password123")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got.ID != u.ID {
		t.Fatalf("Login returned user %d, want %d", got.ID, u.ID)
	}

	// 登出后 token 立即失效
	if err := h.svc.Logout(ctx, token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := h.svc.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expected ErrUnauthenticated after logout, got %v", err)
	}
}

func TestLogin_WrongPasswordAndUnknownUser(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.registerUser(t, "log@example.com", "password123")

	// 两种失败都必须返回同一个错误，避免账号枚举。
	if _, _, err := h.svc.Login(ctx, "log@example.com", "wrongpass"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: got %v", err)
	}
	if _, _, err := h.svc.Login(ctx, "nobody@example.com", "password123"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user: got %v", err)
	}
}

func TestAuthenticate_SessionExpiry(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, token := h.registerUser(t, "s@example.com", "password123")

	// 29 天后仍有效
	h.advance(29 * 24 * time.Hour)
	if _, err := h.svc.Authenticate(ctx, token); err != nil {
		t.Fatalf("session should still be valid at day 29: %v", err)
	}

	// 30 天后失效（与 Python 原版实测的 30 天 TTL 一致）
	h.advance(2 * 24 * time.Hour)
	if _, err := h.svc.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("session should expire after 30 days, got %v", err)
	}
}

func TestAuthenticate_EmptyOrUnknownToken(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for _, tok := range []string{"", "deadbeef"} {
		if _, err := h.svc.Authenticate(ctx, tok); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("Authenticate(%q) = %v, want ErrUnauthenticated", tok, err)
		}
	}
}

func TestPurgeExpiredSessions(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.registerUser(t, "p@example.com", "password123")

	h.advance(31 * 24 * time.Hour)
	n, err := h.svc.PurgeExpiredSessions(ctx)
	if err != nil {
		t.Fatalf("PurgeExpiredSessions: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged %d sessions, want 1", n)
	}
}

func TestHashToken_MatchesPythonScheme(t *testing.T) {
	// sha256("hello") 的十六进制值，验证我们与 Python hashlib.sha256 口径一致。
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got := HashToken("hello"); got != want {
		t.Fatalf("HashToken(\"hello\") = %s, want %s", got, want)
	}
}
