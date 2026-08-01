package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"
)

// migration 是一条已解析的迁移脚本。
type migration struct {
	Version int    // 文件名前缀数字，如 0001 -> 1
	Name    string // 文件名（用于日志与报错定位）
	SQL     string
}

// Migrate 把 fsys 中 dir 目录下的所有迁移按版本号顺序应用到 db。
//
// 设计要点：
//  1. 版本记录表 schema_migrations 由本函数自建，无需外部准备；
//  2. 每条迁移在单个事务内执行——SQLite 的 DDL 是事务性的，
//     因此迁移失败不会留下半成品 schema；
//  3. 已应用过的版本会被跳过，函数因此是幂等的，可在每次启动时无脑调用；
//  4. 不依赖 golang-migrate/goose：整个执行器不到百行，行为完全可控，
//     也避免为一个个人项目引入额外的依赖树。
func Migrate(ctx context.Context, db *sql.DB, fsys fs.FS, dir string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		return fmt.Errorf("store: create schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}

	all, err := loadMigrations(fsys, dir)
	if err != nil {
		return err
	}

	for _, m := range all {
		if applied[m.Version] {
			continue
		}
		if err := applyOne(ctx, db, m); err != nil {
			return err
		}
		logger.InfoContext(ctx, "migration applied",
			slog.String("dir", dir),
			slog.Int("version", m.Version),
			slog.String("name", m.Name),
		)
	}
	return nil
}

// applyOne 在一个事务里执行迁移 SQL 并登记版本号。
func applyOne(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin tx for %s: %w", m.Name, err)
	}
	// defer Rollback：提交成功后再次 Rollback 只会返回 ErrTxDone，忽略即可。
	// 这是 Go 中保证「任何提前 return 都不会泄漏事务」的标准写法。
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("store: exec migration %s: %w", m.Name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`,
		m.Version, m.Name); err != nil {
		return fmt.Errorf("store: record migration %s: %w", m.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit migration %s: %w", m.Name, err)
	}
	return nil
}

// appliedVersions 读出已应用版本号集合。
func appliedVersions(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("store: query schema_migrations: %w", err)
	}
	defer rows.Close()

	out := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("store: scan version: %w", err)
		}
		out[v] = true
	}
	// rows.Err() 必查：迭代中途出错时 Next() 只是返回 false，不会自己报错。
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate schema_migrations: %w", err)
	}
	return out, nil
}

// loadMigrations 读取并解析目录下所有 .sql 文件，按版本号升序返回。
func loadMigrations(fsys fs.FS, dir string) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("store: read migrations dir %q: %w", dir, err)
	}

	var out []migration
	seen := make(map[int]string)

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}

		version, err := parseVersion(name)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("store: duplicate migration version %d: %s and %s", version, prev, name)
		}
		seen[version] = name

		body, err := fs.ReadFile(fsys, dir+"/"+name)
		if err != nil {
			return nil, fmt.Errorf("store: read migration %s: %w", name, err)
		}
		out = append(out, migration{Version: version, Name: name, SQL: string(body)})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// parseVersion 从 "0001_auth.sql" 解析出 1。
func parseVersion(filename string) (int, error) {
	base := strings.TrimSuffix(filename, ".sql")
	idx := strings.Index(base, "_")
	if idx <= 0 {
		return 0, fmt.Errorf("store: migration %q must be named NNNN_description.sql", filename)
	}
	v, err := strconv.Atoi(base[:idx])
	if err != nil {
		return 0, fmt.Errorf("store: migration %q has non-numeric version prefix: %w", filename, err)
	}
	return v, nil
}
