-- 全局库 0001：认证与应用配置。
--
-- 全部使用 IF NOT EXISTS，保证这份迁移可以安全地跑在「原版 Python 遗留的
-- portfolio.db」之上——旧库里这些表已经存在，迁移只是把它们纳入版本管理，
-- 不会破坏任何既有数据。

CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- token_hash = sha256(明文 session token) 的十六进制串。
-- 只存哈希：即使数据库泄露，攻击者也无法反推出可用的 cookie。
CREATE TABLE IF NOT EXISTS auth_sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL,
    expires_at TEXT NOT NULL,              -- UTC 'YYYY-MM-DD HH:MM:SS'
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_auth_sessions_user ON auth_sessions(user_id);

-- 邮箱验证码：每个邮箱同时只保留一条（email 作主键，重发即覆盖）。
CREATE TABLE IF NOT EXISTS email_codes (
    email      TEXT PRIMARY KEY,
    code       TEXT NOT NULL,
    expires_at TEXT NOT NULL,              -- UTC 'YYYY-MM-DD HH:MM:SS'
    attempts   INTEGER DEFAULT 0,
    sent_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- 运行时配置（LLM / 代理 / TDX / 飞书等），key-value 存储。
CREATE TABLE IF NOT EXISTS app_config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
