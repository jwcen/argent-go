// Package store 管理 SQLite 连接：一个全局库 + 每用户一个独立库。
//
// 为什么是「多库」而不是「单库多租户」：这是从 Python 原版继承下来的数据模型。
// 每个用户的业务数据放在 users/u{id}.db，天然做到物理隔离——不可能写错 WHERE
// 就串到别人数据，备份/导出/删号也只是文件级操作。代价是跨用户统计要遍历文件，
// 但对个人理财应用来说这个取舍是划算的。
//
// 本包属于基础设施层：业务域（internal/auth 等）只依赖各自定义的 Repository
// 接口，不会 import 本包，因此依赖方向始终指向内层。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动，无需 cgo，交叉编译友好

	"github.com/jwcen/argent-go/migrations"
)

// Config 描述数据目录布局。
type Config struct {
	// DataDir 数据根目录，默认 "./data"。
	// 全局库位于 <DataDir>/portfolio.db，用户库位于 <DataDir>/users/u{id}.db。
	DataDir string

	// LegacyDB 可选：原版单用户库的路径。
	// 若设置且首个用户（id=1）尚无用户库，则复制该文件作为其初始数据，
	// 对应 API 契约里的「首个用户继承旧库」。
	LegacyDB string
}

// Manager 持有全局库句柄与用户库连接池。
// 零值不可用，必须经 Open 构造。
type Manager struct {
	cfg    Config
	logger *slog.Logger

	global *sql.DB

	mu    sync.RWMutex
	users map[int64]*sql.DB
}

// Open 打开（必要时创建）全局库，跑完全局迁移后返回 Manager。
// 用户库不在此时打开——它们按需惰性打开，避免启动时扫描全部用户。
func Open(ctx context.Context, cfg Config, logger *slog.Logger) (*Manager, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}

	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "users"), 0o755); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}

	globalPath := filepath.Join(cfg.DataDir, "portfolio.db")
	db, err := openSQLite(globalPath)
	if err != nil {
		return nil, err
	}

	if err := Migrate(ctx, db, migrations.Global, migrations.GlobalDir, logger); err != nil {
		_ = db.Close()
		return nil, err
	}

	logger.InfoContext(ctx, "global database ready", slog.String("path", globalPath))

	return &Manager{
		cfg:    cfg,
		logger: logger,
		global: db,
		users:  make(map[int64]*sql.DB),
	}, nil
}

// Global 返回全局库句柄（users / auth_sessions / email_codes / app_config）。
func (m *Manager) Global() *sql.DB { return m.global }

// User 返回指定用户的库句柄，首次访问时打开并跑迁移，之后走缓存。
//
// 并发控制用「双重检查 + 读写锁」：绝大多数调用只需 RLock 命中缓存；
// 只有首次打开才升级到 Lock。升级后必须再查一次 map，因为在释放 RLock 和
// 拿到 Lock 之间，可能已有另一个 goroutine 完成了打开。
func (m *Manager) User(ctx context.Context, userID int64) (*sql.DB, error) {
	m.mu.RLock()
	db, ok := m.users[userID]
	m.mu.RUnlock()
	if ok {
		return db, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if db, ok := m.users[userID]; ok { // 双重检查
		return db, nil
	}

	db, err := m.openUserDB(ctx, userID)
	if err != nil {
		return nil, err
	}
	m.users[userID] = db
	return db, nil
}

// ProvisionUser 为新注册用户准备数据库（建文件 + 跑迁移，必要时继承旧库）。
// 它实现了 auth.Provisioner 接口，由 auth.Service 在注册成功后调用——
// 这样业务层只知道「需要给用户开一个数据空间」，不关心底层是文件还是 schema。
func (m *Manager) ProvisionUser(ctx context.Context, userID int64) error {
	_, err := m.User(ctx, userID)
	return err
}

// openUserDB 完成惰性打开的实际工作。调用方必须已持有写锁。
func (m *Manager) openUserDB(ctx context.Context, userID int64) (*sql.DB, error) {
	path := m.userDBPath(userID)

	// 首个用户继承旧的单用户库：仅当目标文件还不存在时才复制，
	// 保证这个动作是一次性的，重启不会覆盖用户后续产生的数据。
	if userID == 1 && m.cfg.LegacyDB != "" {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := copyFile(m.cfg.LegacyDB, path); err != nil {
				m.logger.WarnContext(ctx, "legacy db inherit skipped",
					slog.String("src", m.cfg.LegacyDB), slog.Any("err", err))
			} else {
				m.logger.InfoContext(ctx, "first user inherited legacy database",
					slog.String("src", m.cfg.LegacyDB), slog.String("dst", path))
			}
		}
	}

	db, err := openSQLite(path)
	if err != nil {
		return nil, err
	}
	if err := Migrate(ctx, db, migrations.User, migrations.UserDir, m.logger); err != nil {
		_ = db.Close()
		return nil, err
	}
	m.logger.InfoContext(ctx, "user database ready",
		slog.Int64("user_id", userID), slog.String("path", path))
	return db, nil
}

func (m *Manager) userDBPath(userID int64) string {
	return filepath.Join(m.cfg.DataDir, "users", fmt.Sprintf("u%d.db", userID))
}

// Close 关闭所有句柄。进程退出前调用。
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	for id, db := range m.users {
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("store: close user db %d: %w", id, err)
		}
		delete(m.users, id)
	}
	if err := m.global.Close(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("store: close global db: %w", err)
	}
	return firstErr
}

// openSQLite 用统一的 PRAGMA 打开一个 SQLite 文件。
//
// PRAGMA 说明：
//   - journal_mode(WAL)：写不阻塞读，是并发场景下的必选项；
//   - busy_timeout(5000)：拿不到锁时最多等 5 秒再报 SQLITE_BUSY，
//     避免瞬时争用直接失败；
//   - foreign_keys(1)：SQLite 默认不强制外键，必须显式打开。
//
// SetMaxOpenConns(1) 是刻意的：把所有访问串行化，从根本上消除 SQLITE_BUSY
// 与写写冲突。对个人理财这种低 QPS 应用，简单和正确远比并发吞吐重要。
// 代价是——在一个事务里再去取第二个连接会死锁，所以事务内必须始终用 tx 对象。
func openSQLite(path string) (*sql.DB, error) {
	dsn := "file:" + path + "?" + url.Values{
		"_pragma": {"journal_mode(WAL)", "busy_timeout(5000)", "foreign_keys(1)"},
	}.Encode()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping %s: %w", path, err)
	}
	return db, nil
}

// copyFile 复制文件内容（用于首个用户继承旧库）。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
