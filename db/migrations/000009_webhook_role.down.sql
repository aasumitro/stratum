ALTER DEFAULT PRIVILEGES FOR ROLE stratum IN SCHEMA ref, organization, account, billing, notification, audit, messaging
REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM stratum_webhook;

REVOKE ALL ON ALL TABLES IN SCHEMA ref, organization, account, billing, notification, audit, messaging FROM stratum_webhook;
REVOKE USAGE ON SCHEMA ref, organization, account, billing, notification, audit, messaging FROM stratum_webhook;
REVOKE CONNECT ON DATABASE stratum FROM stratum_webhook;

DROP ROLE IF EXISTS stratum_webhook;
