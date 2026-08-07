-- 用户库 0003：场外资产 + 定投。
CREATE TABLE IF NOT EXISTS external_assets (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_type        TEXT NOT NULL,          -- FUND / CRYPTO / BOT / WEALTH / CASH / GOLD
    code              TEXT NOT NULL,
    name              TEXT NOT NULL,
    platform          TEXT,
    cost_amount       REAL NOT NULL DEFAULT 0,
    shares            REAL,
    manual_value      REAL,
    note              TEXT,
    okx_algo_id       TEXT,
    okx_bot_type      TEXT,
    annual_yield_rate REAL,
    start_date        TEXT,
    pending_amount    REAL DEFAULT 0,
    bot_budget_override_usdt REAL,
    purchase_fee_rate REAL,
    broker            TEXT,
    closed            INTEGER DEFAULT 0,
    closed_realized   REAL,
    closed_date       TEXT,
    created_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS external_asset_actions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id        INTEGER NOT NULL,
    action_type     TEXT NOT NULL,  -- BUY / ADD / REDEEM / DEPOSIT / WITHDRAW / INTEREST / DIVIDEND
    amount          REAL NOT NULL DEFAULT 0,
    shares          REAL,
    unit_price      REAL,
    fee             REAL DEFAULT 0,
    trade_date      TEXT,
    trade_time      TEXT,
    status          TEXT DEFAULT 'confirmed', -- confirmed / pending
    note            TEXT,
    interest_part   REAL,
    created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_ext_actions_asset
    ON external_asset_actions(asset_id, trade_date);

CREATE TABLE IF NOT EXISTS dca_schedules (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id      INTEGER NOT NULL,
    mode          TEXT NOT NULL DEFAULT 'amount',
    value         REAL NOT NULL,
    frequency     TEXT NOT NULL DEFAULT 'monthly',
    day_of_month  INTEGER,
    day_of_week   INTEGER,
    status        TEXT NOT NULL DEFAULT 'active',
    next_due      TEXT,
    last_fired_at TEXT,
    note          TEXT,
    created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
