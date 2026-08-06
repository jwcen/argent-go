-- 用户库 0002：A 股持仓核心业务表。
--
-- 表结构对齐原版 Python 用户库（通过 .schema 反查），字段名/类型/默认值保持一致，
-- 保证旧库能直接被迁移（IF NOT EXISTS 跳过已存在的表）。
--
-- 核心不变量（wiki/03）：
--   1. position_actions 是真相源，holdings 的 shares/cost_price 可由流水 FIFO 重算
--   2. 任何流水增删改后触发重算（service 层用 ledger.ComputePositionState）

CREATE TABLE IF NOT EXISTS holdings (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    stock_code    TEXT NOT NULL UNIQUE,
    stock_name    TEXT NOT NULL DEFAULT '',
    shares        INTEGER NOT NULL,
    cost_price    REAL NOT NULL,
    purchase_date TEXT,
    broker        TEXT,
    created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS position_actions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    stock_code    TEXT NOT NULL,
    action_type   TEXT NOT NULL,          -- BUY / SELL / ADD
    price         REAL NOT NULL,
    shares        INTEGER NOT NULL,
    tranche_id    INTEGER,
    note          TEXT DEFAULT '',
    trade_date    TEXT,
    trade_time    TEXT,
    fee           REAL,                    -- NULL = 用 estimate_trade_fee 自动算
    broker        TEXT,
    created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS brokers (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT UNIQUE NOT NULL,
    stock_rate  REAL NOT NULL,             -- 佣金费率（万分之几）
    stock_min   REAL NOT NULL,             -- 佣金最低值（元）
    etf_rate    REAL NOT NULL,
    etf_min     REAL NOT NULL,
    is_default  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS position_thesis (
    code        TEXT PRIMARY KEY,
    name        TEXT DEFAULT '',
    thesis      TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS watchlist (
    stock_code  TEXT PRIMARY KEY,
    stock_name  TEXT,
    added_at    TEXT,
    added_price REAL,
    created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS portfolio_snapshots (
    snap_date   TEXT PRIMARY KEY,          -- YYYY-MM-DD
    total_value REAL NOT NULL,
    by_asset    TEXT,                       -- JSON
    created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS cashflow_monthly (
    month         TEXT PRIMARY KEY,         -- YYYY-MM
    income        REAL DEFAULT 0,
    fixed_cost    REAL DEFAULT 0,
    discretionary REAL DEFAULT 0,
    notes         TEXT DEFAULT '',
    created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_position_actions_code
    ON position_actions(stock_code, trade_date);
