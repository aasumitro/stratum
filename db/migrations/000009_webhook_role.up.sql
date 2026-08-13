DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'stratum_webhook') THEN
        CREATE ROLE stratum_webhook LOGIN NOINHERIT BYPASSRLS;
    END IF;
END
$$;

GRANT CONNECT ON DATABASE stratum TO stratum_webhook;
GRANT USAGE ON SCHEMA ref, organization, account, billing, notification, audit, messaging TO stratum_webhook;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA ref, organization, account, billing, notification, audit, messaging TO stratum_webhook;

ALTER DEFAULT PRIVILEGES FOR ROLE stratum IN SCHEMA ref, organization, account, billing, notification, audit, messaging
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO stratum_webhook;
