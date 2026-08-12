DROP POLICY org_isolation ON billing.payment_links;
CREATE POLICY org_isolation ON billing.payment_links
    USING (invoice_id IN (
        SELECT i.id FROM billing.invoices i
        JOIN billing.subscriptions s ON s.id = i.subscription_id
        WHERE s.subject_id = current_setting('app.organization_id', true)
    ));

DROP POLICY org_isolation ON billing.payments;
CREATE POLICY org_isolation ON billing.payments
    USING (invoice_id IN (
        SELECT i.id FROM billing.invoices i
        JOIN billing.subscriptions s ON s.id = i.subscription_id
        WHERE s.subject_id = current_setting('app.organization_id', true)
    ));

DROP POLICY org_isolation ON billing.invoices;
CREATE POLICY org_isolation ON billing.invoices
    USING (subscription_id IN (
        SELECT id FROM billing.subscriptions
        WHERE subject_id = current_setting('app.organization_id', true)
    ));

DROP POLICY org_isolation ON billing.subscriptions;
CREATE POLICY org_isolation ON billing.subscriptions
    USING (subject_id = current_setting('app.organization_id', true));

ALTER TABLE billing.payment_links NO FORCE ROW LEVEL SECURITY;
ALTER TABLE billing.payments NO FORCE ROW LEVEL SECURITY;
ALTER TABLE billing.invoices NO FORCE ROW LEVEL SECURITY;
ALTER TABLE billing.subscriptions NO FORCE ROW LEVEL SECURITY;

ALTER DEFAULT PRIVILEGES FOR ROLE stratum IN SCHEMA ref, organization, account, billing, notification, audit
REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM stratum_app, stratum_worker;

REVOKE ALL ON ALL TABLES IN SCHEMA ref, organization, account, billing, notification, audit FROM stratum_app, stratum_worker;
REVOKE USAGE ON SCHEMA ref, organization, account, billing, notification, audit FROM stratum_app, stratum_worker;
REVOKE CONNECT ON DATABASE stratum FROM stratum_app, stratum_worker;

DROP ROLE IF EXISTS stratum_app;
DROP ROLE IF EXISTS stratum_worker;
