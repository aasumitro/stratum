-- stratum_operator_dev is the local Studio login and the role integration tests use to exercise
-- operator grants and audit capture. Local dev and CI only: its password is public and it never
-- expires. Elsewhere each person gets their own login role, with a generated password and an
-- expiry, in the same group.
--
-- BYPASSRLS is set on the login role itself because Postgres does not inherit role attributes
-- through membership. Table privileges do arrive through the stratum_operator group, which
-- 01-app-role.sql creates.
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'stratum_operator_dev') THEN
        CREATE ROLE stratum_operator_dev LOGIN PASSWORD 'stratum_operator_dev' BYPASSRLS;
    END IF;
END
$$;

GRANT stratum_operator TO stratum_operator_dev;
