-- Substring search for the audit log (index in 000021). pg_trgm ships with PostgreSQL and is a
-- trusted extension, so the database owner can create it, as with pgcrypto in 000002.
--
-- notification.messages gets no trigram index: it runs under FORCE ROW LEVEL SECURITY, and ILIKE is
-- not leakproof, so the planner may never use it as an index condition for the runtime role. The
-- feed's search filters the caller's own rows, found through idx_messages_auth_sub.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
