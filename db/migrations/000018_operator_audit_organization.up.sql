-- An operator's change to an organization's data is recorded with that organization's id, so it
-- shows in the organization's own audit log. Rows recorded before stay NULL: the trail is not
-- rewritten.
--
-- The function runs as its owner, which FORCE ROW LEVEL SECURITY binds when it is not a superuser:
-- the subscription and invoice lookups below would find nothing. That owner gets a read-only policy
-- on those two tables for that. It is the migrator that owns them and could lift FORCE anyway; the
-- runtime roles are unchanged.
DO $$
DECLARE
    fn_owner name;
BEGIN
    SELECT pg_get_userbyid(p.proowner) INTO fn_owner
    FROM pg_catalog.pg_proc p
    JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname = 'audit' AND p.proname = 'capture_operator_change';
    IF fn_owner IS NULL THEN
        RAISE EXCEPTION 'audit.capture_operator_change() is missing; run migration 000011 first';
    END IF;
    EXECUTE format('CREATE POLICY operator_audit_lookup ON billing.subscriptions FOR SELECT TO %I USING (true)', fn_owner);
    EXECUTE format('CREATE POLICY operator_audit_lookup ON billing.invoices FOR SELECT TO %I USING (true)', fn_owner);
END
$$;

CREATE OR REPLACE FUNCTION audit.capture_operator_change() RETURNS trigger
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
    old_row jsonb;
    new_row jsonb;
    key_row jsonb;
    removed_columns CONSTANT text[] := ARRAY['invite_code', 'url', 'owner_id'];
    org_id text;
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

    -- The organization the changed row belongs to, so the change shows in that organization's own
    -- audit log. Catalog and reference tables belong to none and stay NULL, as does a subscription
    -- aimed at a user.
    org_id := CASE TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME
        WHEN 'organization.organizations' THEN key_row->>'id'
        WHEN 'billing.subscriptions' THEN
            CASE WHEN key_row->>'subject_type' = 'organization' THEN key_row->>'subject_id' END
        WHEN 'messaging.outbox' THEN NULLIF(key_row->'payload'->>'org_id', '')
    END;
    IF org_id IS NULL AND key_row ? 'subscription_id' THEN
        SELECT s.subject_id INTO org_id
        FROM billing.subscriptions s
        WHERE s.id = (key_row->>'subscription_id')::uuid AND s.subject_type = 'organization';
    ELSIF org_id IS NULL AND key_row ? 'invoice_id' THEN
        SELECT s.subject_id INTO org_id
        FROM billing.invoices i
        JOIN billing.subscriptions s ON s.id = i.subscription_id
        WHERE i.id = (key_row->>'invoice_id')::uuid AND s.subject_type = 'organization';
    END IF;

    -- A row aimed at one person names them by auth_sub, which must not outlive their account here.
    IF key_row->>'subject_type' = 'user' THEN
        old_row := old_row - 'subject_id';
        new_row := new_row - 'subject_id';
    END IF;

    INSERT INTO audit.events (organization_id, actor, action, resource, status_code, metadata, ip, user_agent)
    VALUES (
        org_id,
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
