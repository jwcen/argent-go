-- 0006_accounts.sql
-- 自定义账户分组：用户可创建「A股 / 支付宝 / 天天基金」等账户，
-- 把持仓和流水归属到不同账户，实现按账户统计汇总。

-- 账户表
CREATE TABLE IF NOT EXISTS accounts (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL UNIQUE,
    kind        TEXT    NOT NULL DEFAULT 'custom',  -- stock / fund / bank / custom
    color       TEXT    DEFAULT '',                 -- 可选颜色标签（hex 或 Tailwind 色名）
    sort_order  INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- holdings 加 account_id（可空，NULL 表示未归类/全部）
ALTER TABLE holdings ADD COLUMN account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL;

-- position_actions 加 account_id（与持仓同步）
ALTER TABLE position_actions ADD COLUMN account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL;
