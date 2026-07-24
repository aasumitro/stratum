CREATE SCHEMA IF NOT EXISTS organization;

CREATE TABLE organization.organizations (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    slug                TEXT        UNIQUE NOT NULL,
    name                TEXT        NOT NULL,
    status              TEXT        NOT NULL DEFAULT 'active'
                                    CHECK (status IN ('active', 'suspended', 'deleted')),
    owner_id            TEXT        NOT NULL,
    invite_code         TEXT        UNIQUE,
    invite_code_enabled BOOLEAN     NOT NULL DEFAULT false,
    timezone            TEXT        NOT NULL DEFAULT 'UTC',
    locale              TEXT        NOT NULL DEFAULT 'en',
    country_code        TEXT        NOT NULL DEFAULT 'US',
    settings            JSONB       NOT NULL DEFAULT '{}',
    suspended_at        TIMESTAMPTZ,
    suspended_reason    TEXT        NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_organizations_owner  ON organization.organizations (owner_id);
CREATE INDEX idx_organizations_status ON organization.organizations (status);

CREATE TABLE organization.memberships (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL REFERENCES organization.organizations(id) ON DELETE CASCADE,
    auth_sub        TEXT        NOT NULL,
    role            TEXT        NOT NULL DEFAULT 'member'
                                CHECK (role IN ('owner', 'admin', 'member')),
    joined_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, auth_sub)
);
CREATE INDEX idx_organization_memberships_organization ON organization.memberships (organization_id);
CREATE INDEX idx_organization_memberships_auth_sub     ON organization.memberships (auth_sub);
CREATE INDEX idx_organization_memberships_org_joined    ON organization.memberships (organization_id, joined_at DESC);

CREATE TABLE organization.invitations (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL REFERENCES organization.organizations(id) ON DELETE CASCADE,
    email           TEXT        NOT NULL,
    role            TEXT        NOT NULL DEFAULT 'member'
                                CHECK (role IN ('admin', 'member')),
    token           TEXT        UNIQUE NOT NULL,
    invited_by      TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending', 'accepted', 'expired')),
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, email)
);
CREATE INDEX idx_invitations_token        ON organization.invitations (token) WHERE status = 'pending';
CREATE INDEX idx_invitations_organization ON organization.invitations (organization_id);

-- secret_plaintext holds the signing secret exactly as generated, never
-- hashed — the worker signs deliveries with it verbatim, so a one-way hash
-- would make that impossible; named to say so rather than imply a
-- protection that was never there.
-- subscribed_events: NULL/empty = every event (same "empty = allow all"
-- convention as organization.settings' allowed_ips).
-- secret_plaintext_previous / secret_rotation_expires_at: 24h dual-signature
-- window on rotation — deliver() signs with the current secret always, plus
-- the previous one until this expires, so the receiver can verify against
-- either while they migrate.
-- auto_disabled_at: system-triggered disable after 3 days of 100% delivery
-- failure — distinct from a plain user-triggered enabled=false, since only
-- this one requires a passing test event before it can be re-enabled.
-- health_warned_at: last time the <70%-success-in-24h owner notification
-- fired, so it doesn't refire on every failure once already below threshold.
CREATE TABLE organization.webhook_endpoints (
    id                         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id            UUID        NOT NULL REFERENCES organization.organizations(id) ON DELETE CASCADE,
    url                        TEXT        NOT NULL,
    secret_plaintext           TEXT        NOT NULL,
    secret_plaintext_previous  TEXT,
    secret_rotation_expires_at TIMESTAMPTZ,
    subscribed_events          TEXT[],
    enabled                    BOOLEAN     NOT NULL DEFAULT true,
    auto_disabled_at           TIMESTAMPTZ,
    health_warned_at           TIMESTAMPTZ,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_webhook_endpoints_organization ON organization.webhook_endpoints (organization_id);

-- attempts_log holds one entry per delivery attempt
-- ({attempt, status_code, latency_ms, error, attempted_at}) so the
-- delivery-detail timeline can render every attempt without a separate
-- attempts table — a retry updates the same logical delivery row rather
-- than creating a new one.
CREATE TABLE organization.webhook_deliveries (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    endpoint_id          UUID        NOT NULL REFERENCES organization.webhook_endpoints(id) ON DELETE CASCADE,
    event_id             TEXT        NOT NULL,
    event_type           TEXT        NOT NULL,
    status               TEXT        NOT NULL DEFAULT 'pending',
    attempts             INT         NOT NULL DEFAULT 0,
    last_error           TEXT,
    response_status_code INT,
    response_body        TEXT,
    latency_ms           INT,
    attempts_log         JSONB       NOT NULL DEFAULT '[]',
    delivered_at         TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_webhook_deliveries_endpoint ON organization.webhook_deliveries (endpoint_id);
CREATE INDEX idx_webhook_deliveries_endpoint_created ON organization.webhook_deliveries (endpoint_id, created_at DESC);

-- Nested folder tree per organization: parent_folder_id is self-referencing,
-- NULL = root-level folder. ON DELETE CASCADE means deleting a folder
-- deletes its subfolders too, but the service layer blocks deleting a
-- non-empty folder (files or subfolders present) before that cascade is
-- ever reached — in practice it only ever fires on an already-empty folder.
CREATE TABLE organization.folders (
    id                UUID        PRIMARY KEY DEFAULT uuidv7(),
    organization_id   UUID        NOT NULL REFERENCES organization.organizations(id) ON DELETE CASCADE,
    parent_folder_id  UUID        REFERENCES organization.folders(id) ON DELETE CASCADE,
    name              TEXT        NOT NULL,
    created_by        TEXT        NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_organization_folders_org_parent ON organization.folders (organization_id, parent_folder_id);

CREATE TABLE organization.files (
    id              UUID        PRIMARY KEY DEFAULT uuidv7(),
    organization_id UUID        NOT NULL REFERENCES organization.organizations(id) ON DELETE CASCADE,
    folder_id       UUID        REFERENCES organization.folders(id) ON DELETE SET NULL,
    name            TEXT        NOT NULL,
    path            TEXT        NOT NULL,
    size_bytes      BIGINT      NOT NULL,
    mime_type       TEXT,
    created_by      TEXT        NOT NULL,
    -- Soft-delete for the 30-day trash. NULL = not deleted. A deleted file
    -- stops counting against the organization's storage quota immediately
    -- (syncStorageUsage only sums deleted_at IS NULL rows) — "delete frees
    -- space, recoverable for 30 days," not bytes-held-until-purged.
    deleted_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_organization_files_organization_id ON organization.files (organization_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_organization_files_folder           ON organization.files (organization_id, folder_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_organization_files_trash            ON organization.files (organization_id, deleted_at) WHERE deleted_at IS NOT NULL;
