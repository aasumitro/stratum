CREATE OR REPLACE FUNCTION audit.anonymize_actor(p_actor text) RETURNS bigint
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

DROP TRIGGER refuse_forged_operator_row ON audit.events;
DROP FUNCTION audit.refuse_forged_operator_row();

DROP TRIGGER audit_operator_change ON billing.payments;
DROP TRIGGER audit_operator_change ON billing.coupon_redemptions;

REVOKE UPDATE (status) ON billing.payments FROM stratum_operator;
REVOKE UPDATE (applied_count) ON billing.coupon_redemptions FROM stratum_operator;
