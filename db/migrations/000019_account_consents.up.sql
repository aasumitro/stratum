-- Each time a user accepts a version of a legal document (terms of service, privacy policy): an
-- append-only history, kept while the account exists and removed with it. Keyed by auth_sub, not a
-- foreign key, because acceptance is recorded before the account.users row is created.
CREATE TABLE account.consents (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    auth_sub    TEXT        NOT NULL,
    document    TEXT        NOT NULL CHECK (document IN ('terms', 'privacy')),
    version     TEXT        NOT NULL,
    accepted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ip          TEXT        NOT NULL DEFAULT '',
    user_agent  TEXT        NOT NULL DEFAULT ''
);
CREATE INDEX idx_account_consents_auth_sub ON account.consents (auth_sub, document, accepted_at DESC);

-- The runtime roles received full access through 000008's default privileges. A record is never
-- changed; only account deletion, which runs in the worker, removes one.
REVOKE UPDATE, DELETE ON account.consents FROM stratum_app, stratum_webhook;
REVOKE UPDATE ON account.consents FROM stratum_worker;
