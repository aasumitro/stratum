CREATE SCHEMA IF NOT EXISTS notification;

CREATE TABLE notification.messages (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL,
    auth_sub        TEXT,
    -- Naming is reversed from intuition: kind = delivery channel
    -- (in_app/push/email), channel = event type (e.g. "invoice_created").
    kind            TEXT        NOT NULL CHECK (kind IN ('in_app', 'push', 'email')),
    channel         TEXT        NOT NULL,
    subject         TEXT        NOT NULL DEFAULT '',
    body            TEXT        NOT NULL,
    payload         JSONB       NOT NULL DEFAULT '{}',
    status          TEXT        NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending', 'sent', 'failed')),
    sent_at         TIMESTAMPTZ,
    read_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_messages_organization ON notification.messages (organization_id);
CREATE INDEX idx_messages_auth_sub     ON notification.messages (auth_sub) WHERE auth_sub IS NOT NULL;
CREATE INDEX idx_messages_status       ON notification.messages (status);
CREATE INDEX idx_messages_unread       ON notification.messages (organization_id, auth_sub) WHERE read_at IS NULL;

CREATE TABLE notification.preferences (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    auth_sub   TEXT        NOT NULL,
    channel    TEXT        NOT NULL CHECK (channel IN ('in_app', 'email', 'push')),
    event_type TEXT        NOT NULL,
    enabled    BOOLEAN     NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (auth_sub, channel, event_type)
);
CREATE INDEX idx_preferences_auth_sub ON notification.preferences (auth_sub);
