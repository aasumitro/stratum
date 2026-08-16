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

-- current_database()/current_user, matching the up migration's reasoning: a literal "stratum"
-- only reverts correctly when the deployment's database and migrator-owner role are named that.
DO $$
BEGIN
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA ref, organization, account, billing, notification, audit, messaging REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM stratum_app, stratum_worker, stratum_webhook', current_user);
END
$$;

REVOKE ALL ON ALL TABLES IN SCHEMA ref, organization, account, billing, notification, audit, messaging FROM stratum_app, stratum_worker, stratum_webhook;
REVOKE USAGE ON SCHEMA ref, organization, account, billing, notification, audit, messaging FROM stratum_app, stratum_worker, stratum_webhook;

DO $$
BEGIN
    EXECUTE format('REVOKE CONNECT ON DATABASE %I FROM stratum_app, stratum_worker, stratum_webhook', current_database());
END
$$;

-- Role creation is not this migration's responsibility (see 000008_db_roles.up.sql), so reverting
-- it does not drop the roles either — their lifecycle belongs to whichever provisioning path
-- created them.
