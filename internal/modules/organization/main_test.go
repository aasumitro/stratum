package organization_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestMain sweeps messaging.outbox for this package's own orphaned test
// debris after every test has finished (and, per t.Cleanup semantics, every
// individual test's own organization.organizations row is already gone).
//
// Almost every test in this package creates an organization through the
// real API, which unconditionally enqueues an OrganizationCreated event
// (and several other flows — member removal, invitation, ownership
// transfer — enqueue their own). messaging.outbox has no FK to
// organizations (it's a generic event queue, can't be scoped that way), so
// deleting a test's org row was never going to sweep its outbox rows too.
// Rather than add that cleanup to each of this package's ~50 test
// functions individually, this sweeps once, package-wide, at the end.
//
// Safety: a row is only deleted when the organization id embedded in its
// own payload no longer exists in organization.organizations. A still-live
// organization's events are therefore never touched, by construction —
// this can only ever remove rows whose organization has already been
// deleted by that test's own cleanup.
func TestMain(m *testing.M) {
	code := m.Run()

	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		if pool, err := pgxpool.New(context.Background(), dsn); err == nil {
			pool.Exec(context.Background(), `
				DELETE FROM messaging.outbox
				WHERE COALESCE(payload->>'org_id', payload->>'organization_id') IS NOT NULL
				  AND NOT EXISTS (
				      SELECT 1 FROM organization.organizations o
				      WHERE o.id::text = COALESCE(payload->>'org_id', payload->>'organization_id')
				  )`)
			pool.Close()
		}
	}

	os.Exit(code)
}
