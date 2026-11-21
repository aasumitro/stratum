DROP INDEX billing.idx_webhook_events_received;
DROP INDEX organization.idx_webhook_deliveries_created;
DROP INDEX notification.idx_messages_created;
DROP INDEX account.idx_login_events_created;

DO $$
DECLARE
    captured regclass;
BEGIN
    FOREACH captured IN ARRAY ARRAY[
        'ref.countries', 'ref.currencies',
        'billing.plans', 'billing.features', 'billing.plan_features',
        'billing.coupons', 'billing.coupon_targets',
        'billing.addons', 'billing.addon_features',
        'billing.subscriptions', 'billing.invoices', 'billing.payment_links',
        'organization.organizations',
        'messaging.outbox'
    ]::regclass[] LOOP
        EXECUTE format('DROP TRIGGER audit_operator_change ON %s', captured);
    END LOOP;
END
$$;

-- Role creation is not this migration's responsibility, so reverting it does not drop
-- stratum_operator; that role's lifecycle belongs to whichever provisioning path created it.
REVOKE ALL ON ALL TABLES IN SCHEMA ref, organization, account, billing, notification, audit, messaging FROM stratum_operator;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA ref, billing FROM stratum_operator;
REVOKE USAGE ON SCHEMA ref, organization, account, billing, notification, audit, messaging FROM stratum_operator;

DO $$
BEGIN
    EXECUTE format('REVOKE CONNECT ON DATABASE %I FROM stratum_operator', current_database());
END
$$;

DROP FUNCTION audit.record_operator_action(text, text, jsonb);
DROP FUNCTION audit.capture_operator_change();
DROP FUNCTION audit.is_operator(name);
DROP FUNCTION audit.anonymize_actor(text);
DROP FUNCTION audit.sweep_expired(int);

-- Restores what 000008 left: the writer's retention delete and the GDPR update ran directly as the
-- runtime roles, so they need these privileges back once the functions above are gone.
DO $$
BEGIN
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA audit GRANT UPDATE, DELETE ON TABLES TO stratum_app, stratum_worker, stratum_webhook', current_user);
END
$$;
GRANT UPDATE, DELETE ON audit.events TO stratum_app, stratum_worker, stratum_webhook;
