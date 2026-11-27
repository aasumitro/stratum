DROP FUNCTION audit.finish_export(bigint, text, timestamptz);
DROP FUNCTION audit.claim_export(int, bigint);
DROP TABLE audit.exports;
REVOKE UPDATE (status, result, error, completed_at) ON account.tasks FROM stratum_worker;
GRANT INSERT, UPDATE ON account.tasks TO stratum_worker;
REVOKE INSERT (organization_id, actor, action, resource, status_code, metadata, ip, user_agent)
    ON audit.events FROM stratum_app, stratum_worker, stratum_webhook;
GRANT INSERT ON audit.events TO stratum_app, stratum_worker, stratum_webhook;
ALTER TABLE audit.events DROP COLUMN export_seq;
