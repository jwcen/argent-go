-- 用户库 0005：除权除息事件表。
--
-- 为什么要单独一张表，而不是继续塞 position_actions：
--   position_actions 记的是「我做了什么」（主观动作，我买/我卖/我收到分红）；
--   dividend_events  记的是「市场发生了什么」（客观事件，某日每股派息 X、送转 Y）。
--   两者的用途完全不同：
--     - 主观 DIVIDEND 流水 → 计入已实现收益（income_realized）
--     - 客观除权事件      → 摊薄持仓成本（DiluteState）+ 前复权价还原（RestoreFromQFQ）
--   同一笔钱只走其中一条路径，混表必然双计。
--
-- 原版 Python 把客观事件放在纯内存缓存里（TTL 12h），冷启动就得重新打外部接口，
-- 且离线不可用。这里落库，行情源只负责增量填充。
--
-- 金额单位：每股（元），已经把交易所口径的「每 10 股派 X 元 / 送转 Y 股」除以 10。

CREATE TABLE IF NOT EXISTS dividend_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    stock_code      TEXT NOT NULL,
    ex_date         TEXT NOT NULL,             -- 除权除息日 YYYY-MM-DD
    cash_per_share  REAL NOT NULL DEFAULT 0,   -- 每股派息（元，含税）
    bonus_ratio     REAL NOT NULL DEFAULT 0,   -- 每股送转率，10送3转2 → 0.5
    source          TEXT NOT NULL DEFAULT 'manual',  -- manual / eastmoney / ...
    note            TEXT DEFAULT '',
    created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(stock_code, ex_date)
);

CREATE INDEX IF NOT EXISTS idx_dividend_events_code
    ON dividend_events(stock_code, ex_date);
