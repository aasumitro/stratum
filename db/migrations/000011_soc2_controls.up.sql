-- Audit history stays trustworthy only if the roles the application connects with cannot rewrite
-- it. Runtime roles keep SELECT and INSERT; the two legitimate rewrites (retention and GDPR
-- anonymization) go through the narrow functions below. The default-privilege revoke keeps a future
-- audit table append-only too. current_user rather than a literal, as in 000008: the migrator's
-- role name differs per deployment.
REVOKE UPDATE, DELETE, TRUNCATE ON audit.events FROM stratum_app, stratum_worker, stratum_webhook;

DO $$
BEGIN
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA audit REVOKE UPDATE, DELETE ON TABLES FROM stratum_app, stratum_worker, stratum_webhook', current_user);
END
$$;

-- Every SECURITY DEFINER function below pins search_path so a caller's temporary or shadowing
-- objects cannot run with the owner's rights, and drops the default PUBLIC EXECUTE.

-- The 365-day floor lives here rather than only in the app's config so that a compromised runtime
-- role cannot shorten the history by passing a small value. One call removes at most 50,000 rows to
-- keep the transaction short; the hourly caller drains anything larger.
CREATE FUNCTION audit.sweep_expired(retention_days int) RETURNS bigint
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
    removed bigint;
BEGIN
    DELETE FROM audit.events
    WHERE id IN (
        SELECT id
        FROM audit.events
        WHERE created_at < now() - make_interval(days => GREATEST(retention_days, 365))
        ORDER BY created_at
        LIMIT 50000
    );
    GET DIAGNOSTICS removed = ROW_COUNT;
    RETURN removed;
END;
$$;
REVOKE ALL ON FUNCTION audit.sweep_expired(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION audit.sweep_expired(int) TO stratum_app;

-- Erases a deleted user's identity from their audit rows and touches nothing else on them. A deleted
-- customer's identity never takes the refused forms, and rewriting those rows would erase what this
-- log exists to keep: who an operator was, and the record of unauthenticated probing.
CREATE FUNCTION audit.anonymize_actor(p_actor text) RETURNS bigint
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
    changed bigint;
BEGIN
    IF p_actor LIKE 'operator:%' OR p_actor IN ('anonymous', 'deleted_user') THEN
        RAISE EXCEPTION 'anonymize_actor: % rows cannot be anonymized', p_actor
            USING ERRCODE = 'insufficient_privilege';
    END IF;

    UPDATE audit.events SET actor = 'deleted_user' WHERE actor = p_actor;
    GET DIAGNOSTICS changed = ROW_COUNT;
    RETURN changed;
END;
$$;
REVOKE ALL ON FUNCTION audit.anonymize_actor(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION audit.anonymize_actor(text) TO stratum_worker;

-- Membership read from the catalog, not pg_has_role: pg_has_role counts every superuser as a member,
-- which would record migrations and owner sessions as operator edits and let an owner connection
-- pass Studio's guard. It follows nested grants, because a login that reaches stratum_operator through
-- another group inherits its write privileges and must not escape capture.
CREATE FUNCTION audit.is_operator(role_name name) RETURNS boolean
    LANGUAGE sql
    STABLE
AS $$
    WITH RECURSIVE granted AS (
        SELECT m.roleid
        FROM pg_catalog.pg_auth_members m
        JOIN pg_catalog.pg_roles login_role ON login_role.oid = m.member
        WHERE login_role.rolname = role_name
        UNION
        SELECT m.roleid
        FROM pg_catalog.pg_auth_members m
        JOIN granted g ON m.member = g.roleid
    )
    SELECT EXISTS (
        SELECT 1
        FROM granted g
        JOIN pg_catalog.pg_roles operator_group ON operator_group.oid = g.roleid
        WHERE operator_group.rolname = 'stratum_operator'
    );
$$;

-- session_user, not current_user: inside a SECURITY DEFINER function current_user is the owner,
-- while session_user stays the role that actually logged in. Application writes leave immediately,
-- so a captured table's normal traffic pays only for this membership check.
--
-- Rows are copied into an audit trail that outlives them, so columns that work as a credential or
-- name a person are removed: an organization's invite code, a payment link's checkout URL, and an
-- organization owner's auth_sub. A secret or personal column added to a captured table later must be
-- added to this list.
CREATE FUNCTION audit.capture_operator_change() RETURNS trigger
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
    old_row jsonb;
    new_row jsonb;
    key_row jsonb;
    removed_columns CONSTANT text[] := ARRAY['invite_code', 'url', 'owner_id'];
BEGIN
    IF NOT audit.is_operator(session_user) THEN
        RETURN NULL;
    END IF;

    IF TG_OP <> 'INSERT' THEN
        old_row := to_jsonb(OLD) - removed_columns;
    END IF;
    IF TG_OP <> 'DELETE' THEN
        new_row := to_jsonb(NEW) - removed_columns;
    END IF;
    key_row := COALESCE(new_row, old_row);

    -- A row aimed at one person names them by auth_sub, which must not outlive their account here.
    IF key_row->>'subject_type' = 'user' THEN
        old_row := old_row - 'subject_id';
        new_row := new_row - 'subject_id';
    END IF;

    INSERT INTO audit.events (organization_id, actor, action, resource, status_code, metadata, ip, user_agent)
    VALUES (
        NULL,
        'operator:' || session_user,
        'studio.' || TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME || '.' || lower(TG_OP),
        TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME || '/' || COALESCE(
            key_row->>'id', key_row->>'code', key_row->>'plan_id', key_row->>'addon_id', key_row->>'coupon_id', ''
        ),
        200,
        jsonb_build_object('old', old_row, 'new', new_row),
        COALESCE(host(inet_client_addr()), ''),
        COALESCE(current_setting('application_name', true), '')
    );
    RETURN NULL;
END;
$$;
REVOKE ALL ON FUNCTION audit.capture_operator_change() FROM PUBLIC;

-- For operator actions no row trigger can see: broker calls, and a broadcast, whose per-recipient
-- rows are deliberately not captured one by one.
CREATE FUNCTION audit.record_operator_action(p_action text, p_resource text, p_metadata jsonb) RETURNS void
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF NOT audit.is_operator(session_user) THEN
        RAISE EXCEPTION 'record_operator_action: % is not an operator role', session_user
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF p_action IS NULL OR p_action NOT LIKE 'studio.%' THEN
        RAISE EXCEPTION 'record_operator_action: action % is outside the studio namespace', p_action
            USING ERRCODE = 'check_violation';
    END IF;

    INSERT INTO audit.events (organization_id, actor, action, resource, status_code, metadata, ip, user_agent)
    VALUES (
        NULL,
        'operator:' || session_user,
        p_action,
        p_resource,
        200,
        COALESCE(p_metadata, '{}'::jsonb),
        COALESCE(host(inet_client_addr()), ''),
        COALESCE(current_setting('application_name', true), '')
    );
END;
$$;
REVOKE ALL ON FUNCTION audit.record_operator_action(text, text, jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION audit.record_operator_action(text, text, jsonb) TO stratum_operator;

-- stratum_operator must already exist: like the runtime roles 000008 grants to, it is created
-- outside migrations (deploy/postgres-init/01-app-role.sql) so the migrator never needs CREATEROLE.
-- The write list is exactly what the operator console writes; no audit, account or webhook-endpoint
-- write is possible through this group.
DO $$
BEGIN
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO stratum_operator', current_database());
END
$$;
GRANT USAGE ON SCHEMA ref, organization, account, billing, notification, audit, messaging TO stratum_operator;
GRANT SELECT ON ALL TABLES IN SCHEMA ref, organization, account, billing, notification, audit, messaging TO stratum_operator;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA ref, billing TO stratum_operator;
GRANT INSERT, UPDATE, DELETE ON
    ref.countries, ref.currencies,
    billing.plans, billing.features, billing.plan_features,
    billing.coupons, billing.coupon_targets,
    billing.addons, billing.addon_features
    TO stratum_operator;
GRANT UPDATE ON billing.subscriptions, billing.invoices, billing.payment_links, organization.organizations TO stratum_operator;
GRANT INSERT ON messaging.outbox, notification.messages TO stratum_operator;

-- Every operator-writable table except notification.messages, where a broadcast inserts one row per
-- recipient and is recorded once through record_operator_action instead.
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
        EXECUTE format(
            'CREATE TRIGGER audit_operator_change AFTER INSERT OR UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION audit.capture_operator_change()',
            captured
        );
    END LOOP;
END
$$;

-- Retention sweeps select by age, oldest first. Without these, every hourly sweep scans and sorts the
-- whole table, and a table large enough to hit the statement timeout would never be swept at all.
CREATE INDEX idx_login_events_created ON account.login_events (created_at);
CREATE INDEX idx_messages_created ON notification.messages (created_at);
CREATE INDEX idx_webhook_deliveries_created ON organization.webhook_deliveries (created_at);
CREATE INDEX idx_webhook_events_received ON billing.webhook_events (received_at);
