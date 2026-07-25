CREATE SCHEMA IF NOT EXISTS billing;

CREATE TABLE billing.features (
    id          TEXT        PRIMARY KEY,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    type        TEXT        NOT NULL CHECK (type IN ('metered', 'boolean', 'static', 'config')),
    metric_key  TEXT,
    active      BOOLEAN     NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((type = 'metered') = (metric_key IS NOT NULL))
);

CREATE TABLE billing.plans (
    id          TEXT        PRIMARY KEY,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    prices      JSONB       NOT NULL DEFAULT '{}',
    sort_order  INT         NOT NULL DEFAULT 0,
    active      BOOLEAN     NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE billing.plan_features (
    plan_id      TEXT   NOT NULL REFERENCES billing.plans(id),
    feature_id   TEXT   NOT NULL REFERENCES billing.features(id),
    limit_value  BIGINT,
    config_value JSONB,
    PRIMARY KEY (plan_id, feature_id)
);
CREATE INDEX idx_plan_features_feature ON billing.plan_features (feature_id);

CREATE TABLE billing.subscriptions (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_type TEXT        NOT NULL CHECK (subject_type IN ('organization', 'user')),
    subject_id   TEXT        NOT NULL,
    plan         TEXT        NOT NULL DEFAULT 'solo' REFERENCES billing.plans(id),
    status       TEXT        NOT NULL DEFAULT 'active'
                             CHECK (status IN ('active', 'trialing', 'cancelled', 'past_due', 'expired')),
    cycle        TEXT        NOT NULL DEFAULT 'monthly'
                             CHECK (cycle IN ('monthly', 'yearly')),
    currency     TEXT        NOT NULL DEFAULT 'USD',
    period_start TIMESTAMPTZ,
    period_end   TIMESTAMPTZ,
    trial_end    TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (subject_type, subject_id)
);
CREATE INDEX idx_subscriptions_subject ON billing.subscriptions (subject_type, subject_id);

CREATE TABLE billing.invoices (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id     UUID        NOT NULL REFERENCES billing.subscriptions(id),
    invoice_number      TEXT,
    amount_cents        BIGINT      NOT NULL CHECK (amount_cents > 0),
    subtotal_cents      BIGINT,
    tax_rate_bps        INT         NOT NULL DEFAULT 0,
    tax_cents           BIGINT      NOT NULL DEFAULT 0,
    currency            TEXT        NOT NULL DEFAULT 'USD',
    status              TEXT        NOT NULL DEFAULT 'pending'
                                    CHECK (status IN ('pending', 'paid', 'failed', 'void')),
    kind                TEXT        NOT NULL DEFAULT 'subscription'
                                    CHECK (kind IN ('subscription', 'extension')),
    -- Only meaningful on an "extension" invoice — whether paying it also
    -- converts the subscription's cycle to yearly (applied by
    -- handleWebhook on payment confirmation, not when the invoice is
    -- created; see service_subscription_billing.go).
    switch_to_annual    BOOLEAN     NOT NULL DEFAULT false,
    provider_invoice_id TEXT,
    due_at              TIMESTAMPTZ,
    paid_at             TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_invoices_subscription ON billing.invoices (subscription_id);
CREATE INDEX idx_invoices_status       ON billing.invoices (status);
-- invoice_number is only unique per subscription (i.e. per organization —
-- one subscription row exists for the lifetime of an org), not globally:
-- it's generated from a per-organization counter (billing.invoice_sequences),
-- so two different organizations' Nth invoice can legitimately share the
-- same formatted number (most commonly, both orgs' very first invoice).
CREATE UNIQUE INDEX idx_invoices_subscription_number ON billing.invoices (subscription_id, invoice_number);

CREATE TABLE billing.invoice_sequences (
    organization_id UUID     NOT NULL,
    year            SMALLINT NOT NULL,
    last_seq        INT      NOT NULL DEFAULT 0,
    PRIMARY KEY (organization_id, year)
);

CREATE TABLE billing.invoice_line_items (
    id               UUID   PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id       UUID   NOT NULL REFERENCES billing.invoices(id),
    description      TEXT   NOT NULL,
    quantity         INT    NOT NULL DEFAULT 1,
    unit_price_cents BIGINT NOT NULL,
    total_cents      BIGINT NOT NULL,
    currency         TEXT   NOT NULL,
    sort_order       INT    NOT NULL DEFAULT 0
);
CREATE INDEX idx_invoice_line_items_invoice ON billing.invoice_line_items (invoice_id);

CREATE TABLE billing.payment_links (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id   UUID        NOT NULL REFERENCES billing.invoices(id),
    provider     TEXT        NOT NULL CHECK (provider IN ('stripe', 'xendit')),
    currency     TEXT        NOT NULL,
    amount_cents BIGINT      NOT NULL CHECK (amount_cents > 0),
    external_id  TEXT,
    url          TEXT,
    status       TEXT        NOT NULL DEFAULT 'pending'
                             CHECK (status IN ('pending', 'paid', 'expired', 'failed')),
    expires_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_payment_links_invoice     ON billing.payment_links (invoice_id);
CREATE INDEX idx_payment_links_external_id ON billing.payment_links (external_id) WHERE external_id IS NOT NULL;

CREATE TABLE billing.subscription_history (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id UUID        NOT NULL REFERENCES billing.subscriptions(id),
    action          TEXT        NOT NULL
                                CHECK (action IN ('trial', 'activate', 'upgrade', 'downgrade', 'cancel', 'resume', 'extend', 'expire')),
    from_plan       TEXT,
    to_plan         TEXT,
    amount_cents    BIGINT      NOT NULL DEFAULT 0,
    currency        TEXT        NOT NULL DEFAULT 'USD',
    changed_by      TEXT        NOT NULL DEFAULT '',
    changed_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    metadata        JSONB       NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_subscription_history_sub ON billing.subscription_history (subscription_id);

CREATE TABLE billing.payments (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id   UUID        NOT NULL REFERENCES billing.invoices(id),
    amount_cents BIGINT      NOT NULL,
    currency     TEXT        NOT NULL,
    provider     TEXT        NOT NULL CHECK (provider IN ('stripe', 'xendit')),
    external_id  TEXT,
    -- 'refunded' is unused by current code — RecordPayment always inserts
    -- the 'completed' default and nothing ever transitions a row to
    -- 'refunded'. There is no refund-initiation feature today; this value
    -- is kept as schema headroom in case a manual Studio-side refund lands.
    status       TEXT        NOT NULL DEFAULT 'completed'
                             CHECK (status IN ('completed', 'refunded')),
    paid_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_payments_invoice ON billing.payments (invoice_id);

CREATE TABLE billing.usage (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL,
    metric          TEXT        NOT NULL,
    value           BIGINT      NOT NULL DEFAULT 0,
    period_start    TIMESTAMPTZ NOT NULL,
    period_end      TIMESTAMPTZ NOT NULL,
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_usage_organization ON billing.usage (organization_id);
CREATE INDEX idx_usage_lookup       ON billing.usage (organization_id, metric, period_start);
CREATE UNIQUE INDEX idx_usage_unique ON billing.usage (organization_id, metric, period_start, period_end);

CREATE TABLE billing.webhook_events (
    provider           TEXT        NOT NULL,
    event_id           TEXT        NOT NULL,
    event_type         TEXT,
    payload_hash       TEXT,
    processing_status  TEXT        NOT NULL DEFAULT 'processed',
    error_message      TEXT,
    received_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, event_id)
);

CREATE TABLE billing.coupons (
    code            TEXT        PRIMARY KEY,
    name            TEXT        NOT NULL,
    discount_type   TEXT        NOT NULL CHECK (discount_type IN ('fixed', 'percent')),
    amount_cents    BIGINT,
    percent_off     SMALLINT,
    currency        TEXT,
    cadence         TEXT        NOT NULL DEFAULT 'once' CHECK (cadence IN ('once', 'repeated', 'forever')),
    duration_count  INT,
    valid_from      TIMESTAMPTZ,
    valid_until     TIMESTAMPTZ,
    max_redemptions INT,
    redeemed_count  INT         NOT NULL DEFAULT 0,
    metadata        JSONB       NOT NULL DEFAULT '{}',
    active          BOOLEAN     NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((discount_type = 'fixed') = (amount_cents IS NOT NULL)),
    CHECK ((discount_type = 'percent') = (percent_off IS NOT NULL)),
    CHECK (percent_off IS NULL OR percent_off BETWEEN 0 AND 100)
);

CREATE TABLE billing.coupon_targets (
    coupon_id    TEXT        NOT NULL REFERENCES billing.coupons(code),
    subject_type TEXT        NOT NULL CHECK (subject_type IN ('organization', 'user')),
    subject_id   TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (coupon_id, subject_type, subject_id)
);

CREATE TABLE billing.coupon_redemptions (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    coupon_id       TEXT        NOT NULL REFERENCES billing.coupons(code),
    subscription_id UUID        NOT NULL REFERENCES billing.subscriptions(id),
    -- Number of invoices this redemption has already discounted — decides
    -- when a cadence='repeated' coupon is exhausted (>= duration_count) and
    -- makes cadence='once' self-consuming after its first invoice.
    applied_count   INT         NOT NULL DEFAULT 0,
    redeemed_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_coupon_redemptions_coupon ON billing.coupon_redemptions (coupon_id);
CREATE INDEX idx_coupon_redemptions_sub    ON billing.coupon_redemptions (subscription_id);

CREATE TABLE billing.addons (
    id          TEXT        PRIMARY KEY,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    prices      JSONB       NOT NULL DEFAULT '{}',
    active      BOOLEAN     NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE billing.addon_features (
    addon_id     TEXT   NOT NULL REFERENCES billing.addons(id),
    feature_id   TEXT   NOT NULL REFERENCES billing.features(id),
    limit_value  BIGINT,
    PRIMARY KEY (addon_id, feature_id)
);
CREATE INDEX idx_addon_features_feature ON billing.addon_features (feature_id);

CREATE TABLE billing.subscription_addons (
    subscription_id UUID        NOT NULL REFERENCES billing.subscriptions(id),
    addon_id        TEXT        NOT NULL REFERENCES billing.addons(id),
    quantity        INT         NOT NULL DEFAULT 1 CHECK (quantity > 0),
    added_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (subscription_id, addon_id)
);
CREATE INDEX idx_subscription_addons_addon ON billing.subscription_addons (addon_id);

ALTER TABLE billing.subscriptions  ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing.invoices       ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing.payments       ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing.payment_links  ENABLE ROW LEVEL SECURITY;

CREATE POLICY org_isolation ON billing.subscriptions
    USING (subject_id = current_setting('app.organization_id', true));

CREATE POLICY org_isolation ON billing.invoices
    USING (subscription_id IN (
        SELECT id FROM billing.subscriptions
        WHERE subject_id = current_setting('app.organization_id', true)
    ));

CREATE POLICY org_isolation ON billing.payments
    USING (invoice_id IN (
        SELECT i.id FROM billing.invoices i
        JOIN billing.subscriptions s ON s.id = i.subscription_id
        WHERE s.subject_id = current_setting('app.organization_id', true)
    ));

CREATE POLICY org_isolation ON billing.payment_links
    USING (invoice_id IN (
        SELECT i.id FROM billing.invoices i
        JOIN billing.subscriptions s ON s.id = i.subscription_id
        WHERE s.subject_id = current_setting('app.organization_id', true)
    ));

-- Catalog seed data — derived from the plans/features this product already
-- displays and already enforces (billing.service.go's checkUsageLimit reads
-- planInfo.Limits[metric] for any metric key, checkFeatureAccess loops
-- planInfo.Features[] for boolean-style gating).
INSERT INTO billing.features (id, name, description, type, metric_key) VALUES
    -- Metered
    ('members', 'Members', 'Number of organization members allowed.', 'metered', 'members'),
    ('storage', 'Storage', 'Total file storage allowed.', 'metered', 'storage_bytes'),
    ('workspaces', 'Workspaces', 'Maximum number of workspaces.', 'metered', 'workspaces'),
    -- Collaboration
    ('teams', 'Teams', 'Maximum number of teams.', 'metered', 'teams'),
    ('roles_permissions', 'Roles & Permissions', 'Create custom roles and fine-grained permissions.', 'boolean', NULL),
    -- API
    ('api_access', 'API Access', 'Access to public API.', 'boolean', NULL),
    ('api_rate_limit', 'API Rate Limit', 'API requests per minute.', 'config', NULL),
    ('personal_access_tokens', 'Personal Access Tokens', 'Generate API tokens.', 'boolean', NULL),
    -- Integrations
    ('webhooks', 'Webhooks', 'Outgoing webhooks.', 'boolean', NULL),
    ('integrations', 'Third-party Integrations', 'Enable external integrations.', 'boolean', NULL),
    -- Security
    ('audit_retention_days', 'Audit Log Retention', 'Audit history retention.', 'config', NULL),
    ('audit_logs', 'Audit Logs', 'Access audit logs.', 'boolean', NULL),
    ('sso', 'Single Sign-On', 'SAML/OIDC login.', 'boolean', NULL),
    ('two_factor_policy', 'Enforce 2FA', 'Require organization-wide MFA.', 'boolean', NULL),
    ('ip_allowlist', 'IP Allowlist', 'Restrict login by IP.', 'boolean', NULL),
    -- Branding
    ('custom_branding', 'Custom Branding', 'Upload logo and branding.', 'boolean', NULL),
    ('custom_domain', 'Custom Domain', 'Serve under your own domain.', 'boolean', NULL),
    -- Analytics
    ('advanced_analytics', 'Advanced Analytics', 'Access to advanced dashboards analytics.', 'boolean', NULL),
    ('export_data', 'Data Export', 'Export reports and data.', 'boolean', NULL),
    -- Support
    ('priority_support', 'Priority Support', 'Faster support response times.', 'boolean', NULL),
    ('dedicated_support', 'Dedicated Support', 'Named support engineer.', 'boolean', NULL),
    -- Enterprise
    ('sla_guarantee', 'SLA Guarantee', 'Enterprise SLA.', 'boolean', NULL),
    ('custom_integrations', 'Custom Integrations', 'Professional integrations.', 'boolean', NULL),
    ('compliance_reports', 'Compliance Reports', 'SOC2/ISO reports.', 'boolean', NULL);

INSERT INTO billing.plans (id, name, description, prices, sort_order) VALUES
    ('solo',  'Solo',  'Everything you need to get started.',
     '{"USD":{"monthly":900,"yearly":9000},"IDR":{"monthly":49000,"yearly":490000}}', 1),
    ('growth', 'Growth', 'For growing teams that need more power.',
     '{"USD":{"monthly":2900,"yearly":29000},"IDR":{"monthly":299000,"yearly":2990000}}', 2),
    ('custom', 'Custom', 'Tailored for enterprises. Talk to us.',
     '{"USD":{"monthly":0,"yearly":0},"IDR":{"monthly":0,"yearly":0}}', 3);

-- api_rate_limit and audit_retention_days values below mirror the actual
-- per-plan limits enforced by the rate-limit middleware: solo:200,
-- growth:1200, custom:unlimited — not invented numbers. A trialing
-- subscription instead gets the middleware's hardcoded trialRateLimit (60),
-- overriding whichever plan it's on; there is no separate 'trial' row here.
INSERT INTO billing.plan_features (plan_id, feature_id, limit_value, config_value) VALUES
    ('solo',  'members', 1, NULL),
    ('solo',  'storage', 250 * 1024 * 1024, NULL),
    ('solo',  'api_rate_limit', NULL, '{"requests_per_minute": 120}'),
    ('solo',  'audit_retention_days', NULL, '{"days": 30}'),
    ('growth', 'members', 15, NULL),
    ('growth', 'storage', 1024 * 1024 * 1024, NULL),
    ('growth', 'priority_support', NULL, NULL),
    ('growth', 'advanced_analytics', NULL, NULL),
    ('growth', 'webhooks', NULL, NULL),
    ('growth', 'api_rate_limit', NULL, '{"requests_per_minute": 720}'),
    ('growth', 'audit_retention_days', NULL, '{"days": 90}'),
    ('custom', 'members', -1, NULL),
    ('custom', 'storage', -1, NULL),
    ('custom', 'priority_support', NULL, NULL),
    ('custom', 'dedicated_support', NULL, NULL),
    ('custom', 'advanced_analytics', NULL, NULL),
    ('custom', 'custom_integrations', NULL, NULL),
    ('custom', 'sla_guarantee', NULL, NULL),
    ('custom', 'webhooks', NULL, NULL),
    ('custom', 'sso', NULL, NULL),
    ('custom', 'custom_domain', NULL, NULL),
    ('custom', 'api_rate_limit', NULL, '{"requests_per_minute": -1}'),
    ('custom', 'audit_retention_days', NULL, '{"days": -1}'),
    -- The rest of the catalog (unlimited counts for the two metered
    -- features, presence-only for the booleans) — solo/growth are
    -- unchanged, custom is the top tier so it grants everything else.
    ('custom', 'workspaces', -1, NULL),
    ('custom', 'teams', -1, NULL),
    ('custom', 'roles_permissions', NULL, NULL),
    ('custom', 'api_access', NULL, NULL),
    ('custom', 'personal_access_tokens', NULL, NULL),
    ('custom', 'integrations', NULL, NULL),
    ('custom', 'audit_logs', NULL, NULL),
    ('custom', 'two_factor_policy', NULL, NULL),
    ('custom', 'ip_allowlist', NULL, NULL),
    ('custom', 'custom_branding', NULL, NULL),
    ('custom', 'export_data', NULL, NULL),
    ('custom', 'compliance_reports', NULL, NULL);

-- Unit-based addons.
-- Quantity is stored in subscription_addons.
-- Example:
-- extra-seat x5 = +5 members
-- extra-storage-1gb x10 = +10GB storage
INSERT INTO billing.addons (id, name, description, prices) VALUES
('extra-seat', '+1 Member', '1 additional member seat added to your plan''s limit.',
 '{"USD": {"monthly": 100, "yearly": 1000}, "IDR": {"monthly": 10000, "yearly": 100000}}'),
 ('extra-storage-1gb', '+1 GB Storage', '1 GB of additional file storage added to your plan''s limit.',
 '{"USD": {"monthly": 100, "yearly": 1000}, "IDR": {"monthly": 10000, "yearly": 100000}}');

INSERT INTO billing.addon_features (addon_id, feature_id, limit_value) VALUES
    ('extra-seat', 'members', 1),
    ('extra-storage-1gb', 'storage', 1024 * 1024 * 1024);
