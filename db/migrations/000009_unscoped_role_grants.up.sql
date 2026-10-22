-- stratum_app_unscoped (BYPASSRLS) must already exist: like the other runtime roles it is created
-- outside migrations (deploy/postgres-init/01-app-role.sql), so the migrator never needs BYPASSRLS
-- itself. cmd/api uses it only for the organization paths that have no tenant to scope a
-- transaction to — the organization middleware's authorization lookups and the cross-org
-- invite/join flows — so it gets the organization schema and the outbox those flows enqueue into,
-- and nothing else: no billing, account or notification access.
GRANT USAGE ON SCHEMA organization, messaging TO stratum_app_unscoped;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA organization TO stratum_app_unscoped;
GRANT SELECT, INSERT ON messaging.outbox TO stratum_app_unscoped;

DO $$
BEGIN
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA organization GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO stratum_app_unscoped', current_user);
END
$$;
