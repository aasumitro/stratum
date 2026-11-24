-- An email held for its recipient's daily digest instead of sent at once ('held'). The index the
-- hourly digest sender reads them through is built concurrently in 000023.
ALTER TABLE notification.messages DROP CONSTRAINT messages_status_check;
-- NOT VALID: the new check only allows more values, so every existing row already satisfies it and
-- the full-table scan under ACCESS EXCLUSIVE is skipped. New and updated rows are checked as usual.
ALTER TABLE notification.messages ADD CONSTRAINT messages_status_check
    CHECK (status IN ('pending', 'sent', 'failed', 'held')) NOT VALID;
