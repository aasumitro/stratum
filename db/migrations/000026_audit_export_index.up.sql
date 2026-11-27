-- Finds both the rows still to be exported (export_seq IS NULL, oldest first) and the rows of one
-- export. Built without blocking writes: CONCURRENTLY cannot run inside a transaction, which is why
-- this file holds this one statement alone. No IF NOT EXISTS: a failed concurrent build leaves an
-- INVALID index behind, and a retry must fail on it rather than skip it (recovery in
-- docs/12-operations.md).
CREATE INDEX CONCURRENTLY idx_audit_export ON audit.events (export_seq, created_at);
