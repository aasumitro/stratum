-- Studio's Void gives back a voided invoice's coupon application, as the app's own void does, and an
-- operator records a refund made at the provider for a payment that settled nothing. Each grant is
-- the one column those writes change, and both tables are captured like every other table an
-- operator writes.
GRANT UPDATE (applied_count) ON billing.coupon_redemptions TO stratum_operator;
GRANT UPDATE (status) ON billing.payments TO stratum_operator;

CREATE TRIGGER audit_operator_change AFTER INSERT OR UPDATE OR DELETE ON billing.coupon_redemptions
    FOR EACH ROW EXECUTE FUNCTION audit.capture_operator_change();
CREATE TRIGGER audit_operator_change AFTER INSERT OR UPDATE OR DELETE ON billing.payments
    FOR EACH ROW EXECUTE FUNCTION audit.capture_operator_change();

-- An operator row is written only by capture_operator_change and record_operator_action, which run as
-- the owner of audit.events. Runtime roles keep INSERT for the application's own rows, so without this
-- a stolen runtime credential could write a row naming an operator. A role holding the owner's
-- privileges passes: it could drop this trigger anyway. SECURITY INVOKER on purpose: current_user here
-- is the role that inserted.
CREATE FUNCTION audit.refuse_forged_operator_row() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF NEW.actor LIKE 'operator:%' AND NOT pg_has_role(current_user, (
        SELECT c.relowner FROM pg_class c WHERE c.oid = 'audit.events'::regclass
    ), 'USAGE') THEN
        RAISE EXCEPTION 'audit.events: % may not write operator rows', current_user
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION audit.refuse_forged_operator_row() FROM PUBLIC;

CREATE TRIGGER refuse_forged_operator_row BEFORE INSERT ON audit.events
    FOR EACH ROW EXECUTE FUNCTION audit.refuse_forged_operator_row();

-- Anonymization also clears where the request came from and with what client: both identify the
-- person as much as the actor does, and would otherwise stay for the whole retention period.
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

    UPDATE audit.events SET actor = 'deleted_user', ip = '', user_agent = '' WHERE actor = p_actor;
    GET DIAGNOSTICS changed = ROW_COUNT;
    RETURN changed;
END;
$$;
