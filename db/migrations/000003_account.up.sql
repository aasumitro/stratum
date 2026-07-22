CREATE SCHEMA IF NOT EXISTS account;

CREATE TABLE account.users (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    auth_sub     TEXT        UNIQUE NOT NULL,
    email        TEXT        UNIQUE NOT NULL,
    full_name    TEXT        NOT NULL DEFAULT '',
    avatar_url   TEXT        NOT NULL DEFAULT '',
    preferences  JSONB       NOT NULL DEFAULT '{}',
    mfa_enabled  BOOLEAN     NOT NULL DEFAULT false,
    last_seen_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_users_auth_sub ON account.users (auth_sub);

CREATE TABLE account.tasks (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    auth_sub     TEXT        NOT NULL,
    kind         TEXT        NOT NULL CHECK (kind IN ('delete_account', 'export_data')),
    status       TEXT        NOT NULL DEFAULT 'pending'
                             CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    result       JSONB,
    error        TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);
CREATE INDEX idx_account_tasks_auth_sub ON account.tasks (auth_sub);

CREATE TABLE account.login_events (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    auth_sub    TEXT        NOT NULL,
    ip_address  TEXT        NOT NULL DEFAULT '',
    user_agent  TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_login_events_auth_sub ON account.login_events (auth_sub, created_at DESC);
