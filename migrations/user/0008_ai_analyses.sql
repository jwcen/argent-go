-- 用户库 0008：AI 个股分析历史（用于后验复盘）。
-- 每次结构化 AI 分析持久化一条，记录方向判断与当时现价，
-- 之后可据此复盘「当时看多/看空，实际涨跌如何」。
CREATE TABLE IF NOT EXISTS ai_analyses (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    code       TEXT NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    direction  TEXT NOT NULL DEFAULT '',
    advice     TEXT NOT NULL DEFAULT '',
    trigger    TEXT NOT NULL DEFAULT '',
    risk       TEXT NOT NULL DEFAULT '',
    price_at   REAL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
