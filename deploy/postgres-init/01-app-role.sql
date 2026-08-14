-- Creates the three roles db/migrations/000008_db_roles.up.sql grants privileges to but does not
-- create itself, so that migration never needs a migrator role holding BYPASSRLS or CREATEROLE.
-- Three consumers reuse this exact file:
--   1. Local dev — mounted into the Postgres container's docker-entrypoint-initdb.d, runs
--      automatically on first boot, before any migration.
--   2. CI — run as an explicit step (`psql -f`) immediately before `migrate ... up`.
--   3. Staging/production — copy-pasted by an operator with a superuser-equivalent connection,
--      substituting real generated passwords for the hardcoded local-dev ones below. See
--      docs/12-operations.md's "Provisioning database roles" runbook.
-- 02-test-role.sql (same directory) is a separate, test-only concern — never part of a real
-- deployment — kept in its own numbered file rather than folded in here for exactly that reason.
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
END
$$;
