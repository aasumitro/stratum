CREATE TABLE projects (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    api_url      TEXT NOT NULL,
    db_dsn       TEXT NOT NULL,
    mq_dsn       TEXT NOT NULL,
    redis_dsn    TEXT,
    color        TEXT NOT NULL DEFAULT '#6366f1',
    created_at   DATETIME NOT NULL DEFAULT (datetime('now')),
    last_seen_at DATETIME
);

CREATE TABLE monitor_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id TEXT    NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    status     TEXT    NOT NULL,
    latency_ms INTEGER,
    checked_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
