-- Creates the roles db/migrations/000008_db_roles.up.sql, 000009_unscoped_role_grants.up.sql and
-- 000011_soc2_controls.up.sql grant privileges to but do not
-- create itself, so that migration never needs a migrator role holding BYPASSRLS or CREATEROLE.
-- Three consumers reuse this exact file:
--   1. Local dev — mounted into the Postgres container's docker-entrypoint-initdb.d, runs
--      automatically on first boot, before any migration.
--   2. CI — run as an explicit step (`psql -f`) immediately before `migrate ... up`.
--   3. Staging/production — copy-pasted by an operator with a superuser-equivalent connection,
--      substituting real generated passwords for the hardcoded local-dev ones below. See
--      docs/12-operations.md's "Provisioning database roles" runbook.
-- 02-test-role.sql and 03-operator-role.sql (same directory) are separate, local-only concerns —
-- never part of a real deployment — kept in their own numbered files for exactly that reason.
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'stratum_app') THEN
        CREATE ROLE stratum_app LOGIN PASSWORD 'stratum_app';
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'stratum_worker') THEN
        CREATE ROLE stratum_worker LOGIN PASSWORD 'stratum_worker' BYPASSRLS;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'stratum_webhook') THEN
        CREATE ROLE stratum_webhook LOGIN PASSWORD 'stratum_webhook' BYPASSRLS;
    END IF;
    -- cmd/api only: the organization paths with no tenant to scope a
    -- transaction to (authorization lookups, cross-org invite/join flows).
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'stratum_app_unscoped') THEN
        CREATE ROLE stratum_app_unscoped LOGIN PASSWORD 'stratum_app_unscoped' BYPASSRLS;
    END IF;
    -- The group every operator's own login role joins. It cannot log in and holds no credential,
    -- so it is safe everywhere this file runs; migration 000011 grants to it.
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'stratum_operator') THEN
        CREATE ROLE stratum_operator NOLOGIN;
    END IF;
END
$$;
