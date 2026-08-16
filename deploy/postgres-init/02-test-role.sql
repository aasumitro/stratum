-- stratum_test is test-only infrastructure — never provision this role outside local dev/CI, and
-- never reference it from db/migrations or docs/12-operations.md's production runbook. It exists
-- because TEST_DATABASE_URL needs a role that can do everything the old superuser connection did
-- by identity: bypass RLS (to seed FORCE ROW LEVEL SECURITY billing tables directly) and create,
-- trigger, and drop objects it created in billing/public (several billing integration tests create
-- a throwaway trigger function, attach it, then drop both in t.Cleanup, to inject a
-- mid-transaction failure).
--
-- Two grants, not a pile of per-privilege ones:
--   - BYPASSRLS is a role attribute, not an inheritable privilege, so it must be set directly.
--   - Membership in `stratum` (the schema owner) gives stratum_test everything ownership implies —
--     DML without a separate GRANT, and critically DROP TRIGGER/DROP FUNCTION rights on objects
--     `stratum` owns, since PostgreSQL checks those against table/object ownership specifically,
--     not against CREATE/TRIGGER privilege. A first attempt granted CREATE + TRIGGER on the schema
--     explicitly instead of this membership: creating a trigger worked, but t.Cleanup's `DROP
--     TRIGGER` then failed with permission denied (silently — the test helpers don't check that
--     error), leaving the trigger attached and breaking every later test that touched the same
--     table. Role membership sidesteps the whole ownership-vs-privilege distinction, and — because
--     it's a role relationship, not an object grant — needs no post-migration step: it's correct
--     immediately, before any schema in this file even exists yet.
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'stratum_test') THEN
        CREATE ROLE stratum_test LOGIN PASSWORD 'stratum_test' BYPASSRLS;
    END IF;
END
$$;

GRANT stratum TO stratum_test;
