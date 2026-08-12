DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'stratum_app') THEN
        CREATE ROLE stratum_app LOGIN NOINHERIT;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'stratum_worker') THEN
        CREATE ROLE stratum_worker LOGIN NOINHERIT BYPASSRLS;
    END IF;
END
$$;

GRANT CONNECT ON DATABASE stratum TO stratum_app, stratum_worker;
GRANT USAGE ON SCHEMA ref, organization, account, billing, notification, audit TO stratum_app, stratum_worker;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA ref, organization, account, billing, notification, audit TO stratum_app, stratum_worker;

ALTER DEFAULT PRIVILEGES FOR ROLE stratum IN SCHEMA ref, organization, account, billing, notification, audit
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO stratum_app, stratum_worker;

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
