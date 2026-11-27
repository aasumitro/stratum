DROP POLICY operator_audit_lookup ON billing.invoices;
DROP POLICY operator_audit_lookup ON billing.subscriptions;

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
