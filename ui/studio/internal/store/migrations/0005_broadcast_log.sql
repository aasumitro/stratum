CREATE TABLE IF NOT EXISTS broadcast_log (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    project_id      TEXT     NOT NULL,
    title           TEXT     NOT NULL,
    body            TEXT     NOT NULL,
    target          TEXT     NOT NULL,  -- 'all' | 'workspace:<id>' | 'user:<auth_sub>'
    recipient_count INTEGER  NOT NULL DEFAULT 0,
    sent_at         DATETIME NOT NULL DEFAULT (datetime('now'))
);
