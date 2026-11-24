-- audit.events is large and append-only, so its index is built without blocking writes. CONCURRENTLY
-- cannot run inside a transaction, which is why this file holds this one statement alone. No
-- IF NOT EXISTS: a failed concurrent build leaves an INVALID index behind, and a retry must fail on
-- it rather than skip it (recovery in docs/12-operations.md).
CREATE INDEX CONCURRENTLY idx_audit_search ON audit.events
    USING gin ((resource || ' ' || metadata::text) gin_trgm_ops);
