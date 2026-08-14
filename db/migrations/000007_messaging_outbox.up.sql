CREATE SCHEMA IF NOT EXISTS messaging;

CREATE TABLE messaging.outbox (
    id           UUID        PRIMARY KEY,           -- same ID as the Envelope's own id (one row = one envelope)
    exchange     TEXT        NOT NULL,
    routing_key  TEXT        NOT NULL,
    payload      JSONB       NOT NULL,               -- the fully marshaled Envelope, ready to publish verbatim
    not_before   TIMESTAMPTZ NOT NULL DEFAULT now(), -- PublishDelayed's delay folded into a ready-timestamp, not a separate mechanism
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,                        -- NULL = not yet published
    attempts     INT         NOT NULL DEFAULT 0,
    last_error   TEXT
);
CREATE INDEX idx_outbox_unpublished ON messaging.outbox (not_before) WHERE published_at IS NULL;
