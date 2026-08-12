DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'stratum_app') THEN
        CREATE ROLE stratum_app LOGIN PASSWORD 'stratum_app';
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'stratum_worker') THEN
        CREATE ROLE stratum_worker LOGIN PASSWORD 'stratum_worker' BYPASSRLS;
    END IF;
END
$$;
