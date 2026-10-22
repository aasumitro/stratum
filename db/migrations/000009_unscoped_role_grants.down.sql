DO $$
BEGIN
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA organization REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM stratum_app_unscoped', current_user);
END
$$;

REVOKE SELECT, INSERT ON messaging.outbox FROM stratum_app_unscoped;
REVOKE SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA organization FROM stratum_app_unscoped;
REVOKE USAGE ON SCHEMA organization, messaging FROM stratum_app_unscoped;
