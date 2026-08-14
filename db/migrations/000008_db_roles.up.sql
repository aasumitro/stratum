-- stratum_app, stratum_worker, and stratum_webhook are provisioned outside this migration —
-- automatically in local dev (deploy/postgres-init/01-app-role.sql, run by the Postgres
-- container's docker-entrypoint-initdb.d before any migration), by CI (same script, run as an
-- explicit step before `migrate up`), and manually by an operator with a superuser-equivalent
-- connection in every other environment (docs/12-operations.md's "Provisioning database roles"
-- runbook, before the first `make migrate-up`). This is what lets the migrator role itself run
-- without BYPASSRLS or CREATEROLE — only the roles it grants privileges to below need to already
-- exist; PostgreSQL only permits a role that already holds BYPASSRLS to grant BYPASSRLS to a role
-- it creates, so creating stratum_worker/stratum_webhook here would force the migrator itself to
-- hold that same elevated privilege.

-- current_database() and current_user, not the literal "stratum": PostgreSQL can't parameterise
-- an identifier in plain DDL, so a literal database/role name only works when the deployment's
-- database and migrator-owner role happen to be called "stratum". current_database() always names
-- the database this connection is actually on; current_user is the migrator's own role, which the
-- "Provisioning database roles" runbook already requires to own the schemas being migrated.
DO $$
BEGIN
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO stratum_app, stratum_worker, stratum_webhook', current_database());
END
$$;
GRANT USAGE ON SCHEMA ref, organization, account, billing, notification, audit, messaging TO stratum_app, stratum_worker, stratum_webhook;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA ref, organization, account, billing, notification, audit, messaging TO stratum_app, stratum_worker, stratum_webhook;

DO $$
BEGIN
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA ref, organization, account, billing, notification, audit, messaging GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO stratum_app, stratum_worker, stratum_webhook', current_user);
END
$$;

ALTER TABLE billing.subscriptions FORCE ROW LEVEL SECURITY;
ALTER TABLE billing.invoices FORCE ROW LEVEL SECURITY;
ALTER TABLE billing.payments FORCE ROW LEVEL SECURITY;
ALTER TABLE billing.payment_links FORCE ROW LEVEL SECURITY;

ALTER POLICY org_isolation ON billing.subscriptions WITH CHECK (subject_id = current_setting('app.organization_id', true));

ALTER POLICY org_isolation ON billing.invoices WITH CHECK (subscription_id IN (
    SELECT id FROM billing.subscriptions
    WHERE subject_id = current_setting('app.organization_id', true)
));

ALTER POLICY org_isolation ON billing.payments WITH CHECK (invoice_id IN (
    SELECT i.id FROM billing.invoices i
    JOIN billing.subscriptions s ON s.id = i.subscription_id
    WHERE s.subject_id = current_setting('app.organization_id', true)
));

ALTER POLICY org_isolation ON billing.payment_links WITH CHECK (invoice_id IN (
    SELECT i.id FROM billing.invoices i
    JOIN billing.subscriptions s ON s.id = i.subscription_id
    WHERE s.subject_id = current_setting('app.organization_id', true)
));
