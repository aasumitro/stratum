-- Emails still held would never be sent by the code before this migration: record them as failed
-- rather than leave them waiting. FORCE ROW LEVEL SECURITY binds a non-superuser migrator, so it is
-- lifted for that one update.
ALTER TABLE notification.messages NO FORCE ROW LEVEL SECURITY;
UPDATE notification.messages SET status = 'failed', updated_at = now() WHERE status = 'held';
ALTER TABLE notification.messages FORCE ROW LEVEL SECURITY;

ALTER TABLE notification.messages DROP CONSTRAINT messages_status_check;
ALTER TABLE notification.messages ADD CONSTRAINT messages_status_check
    CHECK (status IN ('pending', 'sent', 'failed'));
