-- 用户库 0007：自选池支持 股票/基金 两类。
-- 原 watchlist 只有 stock_code 主键，无法区分基金（基金与 A 股同为 6 位代码）。
-- 改为复合主键 (item_type, code)，并把列名 stock_code/stock_name 规范成 code/name。
CREATE TABLE IF NOT EXISTS watchlist_new (
    item_type   TEXT NOT NULL DEFAULT 'STOCK',  -- STOCK / FUND
    code        TEXT NOT NULL,
    name        TEXT,
    added_at    TEXT,
    added_price REAL,
    created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (item_type, code)
);

INSERT OR IGNORE INTO watchlist_new (item_type, code, name, added_at, added_price, created_at)
    SELECT 'STOCK', stock_code, stock_name, added_at, added_price, created_at
    FROM watchlist;

DROP TABLE watchlist;

ALTER TABLE watchlist_new RENAME TO watchlist;
