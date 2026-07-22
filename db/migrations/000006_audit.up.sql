CREATE SCHEMA IF NOT EXISTS audit;

CREATE TABLE audit.events (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id TEXT,
    actor           TEXT        NOT NULL,
    action          TEXT        NOT NULL,
    resource        TEXT        NOT NULL,
    status_code     INT         NOT NULL,
    metadata        JSONB       NOT NULL DEFAULT '{}',
    ip              TEXT        NOT NULL DEFAULT '',
    user_agent      TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_organization ON audit.events (organization_id) WHERE organization_id IS NOT NULL;
CREATE INDEX idx_audit_actor        ON audit.events (actor);
CREATE INDEX idx_audit_created      ON audit.events (created_at);

-- Restrict audit.events to append-only for the application role. Wrapped so
-- the migration doesn't fail where the stratum_app role hasn't been
-- created yet (e.g. local/dev infra without a dedicated app role).
DO $$
BEGIN
    REVOKE DELETE, UPDATE ON audit.events FROM stratum_app;
EXCEPTION
    WHEN undefined_object THEN NULL;
END;
$$;
