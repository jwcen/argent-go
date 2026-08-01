-- 用户库 0001：每用户独立数据库的骨架。
--
-- Stage 2 只保证「用户库能被创建并纳入版本管理」，业务表（holdings /
-- position_actions / external_assets ...）由后续 Stage 以 0002、0003…
-- 增量迁移加入。这样每个 Stage 的边界是清晰的，迁移历史也如实反映演进过程。
--
-- 同样使用 IF NOT EXISTS：首个用户会继承旧的单用户库，里面已有全套业务表。

CREATE TABLE IF NOT EXISTS app_config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
