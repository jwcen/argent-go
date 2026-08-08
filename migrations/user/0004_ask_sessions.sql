-- 用户库 0004：问问市场（ask）会话持久化。
-- 会话与消息都归属当前用户，存在各用户独立库（users/u{id}.db）中。
CREATE TABLE IF NOT EXISTS ask_sessions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_ask_sessions_user ON ask_sessions(user_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS ask_messages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER NOT NULL,
    user_id    INTEGER NOT NULL,
    role       TEXT NOT NULL,       -- user / assistant
    content    TEXT NOT NULL DEFAULT '',
    meta       TEXT,                -- JSON: {images, tools_used, sources, charts}
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_ask_messages_session ON ask_messages(session_id, id);
