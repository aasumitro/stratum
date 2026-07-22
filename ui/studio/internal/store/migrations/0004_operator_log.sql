CREATE TABLE IF NOT EXISTS operator_log (
    id           INTEGER  PRIMARY KEY AUTOINCREMENT,
    project_id   TEXT     NOT NULL,
    action       TEXT     NOT NULL,
    target_id    TEXT     NOT NULL,
    detail       TEXT,
    performed_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
