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
