-- notification.messages keeps 180 days of messages, so this index is built without blocking writes.
-- CONCURRENTLY cannot run inside a transaction, which is why this file holds this one statement alone.
-- No IF NOT EXISTS: a failed build leaves an INVALID index that a retry must fail on, not skip
-- (recovery as for 000021 in docs/12-operations.md).
CREATE INDEX CONCURRENTLY idx_messages_held ON notification.messages (auth_sub, created_at) WHERE status = 'held';
