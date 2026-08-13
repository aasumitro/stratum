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

-- messaging is a new schema, unlike 000007_runtime_role's cross-schema DDL
-- against schemas that already had these grants — without this, both
-- stratum_app (API) and stratum_worker (worker) get "permission denied for
-- table outbox" on their very first events.Enqueue call.
GRANT USAGE ON SCHEMA messaging TO stratum_app, stratum_worker;
GRANT SELECT, INSERT, UPDATE, DELETE ON messaging.outbox TO stratum_app, stratum_worker;
