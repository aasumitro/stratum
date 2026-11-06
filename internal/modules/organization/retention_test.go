package organization_test

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/organization"
)

// These tests share a database with local development, and a sweep touches every
// row past its cutoff. A cutoff a century back keeps a run from removing anything
// but the rows it seeded.
const (
	sweepDays = 36500
	expired   = sweepDays + 1
	current   = sweepDays - 1
	// Retention above sweepDays, for the table a test is not looking at.
	keepAll = 73000
)

func orgWorkerModule(t *testing.T) (admin *pgxpool.Pool, mod *organization.Module) {
	t.Helper()
	admin = testPool(t)
	dsn := os.Getenv("POSTGRES_WORKER_URL")
	if dsn == "" {
		t.Skip("POSTGRES_WORKER_URL not set")
	}
	worker, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open worker pool: %v", err)
	}
	t.Cleanup(worker.Close)
	return admin, organization.New(worker, "test-webhook-secret-encryption-key-0000", "", 1)
}

// seedRetentionOrg inserts an organization with the given status, last changed
// ageDays ago, and removes it after the test if a sweep did not.
func seedRetentionOrg(t *testing.T, admin *pgxpool.Pool, slug, status string, ageDays int) string {
	t.Helper()
	var id string
	if err := admin.QueryRow(t.Context(), `
		INSERT INTO organization.organizations (slug, name, owner_id, status, updated_at)
		VALUES ($1, $1, 'retention_owner', $2, now() - make_interval(days => $3))
		RETURNING id`, slug, status, ageDays).Scan(&id); err != nil {
		t.Fatalf("seed organization %s: %v", slug, err)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, id)
	})
	return id
}

func orgExists(t *testing.T, admin *pgxpool.Pool, id string) bool {
	t.Helper()
	var exists bool
	if err := admin.QueryRow(t.Context(),
		`SELECT EXISTS (SELECT 1 FROM organization.organizations WHERE id = $1)`, id).Scan(&exists); err != nil {
		t.Fatalf("read organization: %v", err)
	}
	return exists
}

func TestSweepRetention_PurgeRespectsTheGracePeriod(t *testing.T) {
	admin, mod := orgWorkerModule(t)
	purged := seedRetentionOrg(t, admin, "retention-deleted-old", "deleted", expired)
	kept := seedRetentionOrg(t, admin, "retention-deleted-recent", "deleted", current)

	if _, err := mod.SweepRetention(t.Context(), keepAll, sweepDays); err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}

	if orgExists(t, admin, purged) {
		t.Error("an organization past the grace period must be purged")
	}
	if !orgExists(t, admin, kept) {
		t.Error("an organization inside the grace period must stay")
	}
}

func TestSweepRetention_PurgeNeverTouchesALiveOrganization(t *testing.T) {
	admin, mod := orgWorkerModule(t)
	active := seedRetentionOrg(t, admin, "retention-active-old", "active", expired)
	suspended := seedRetentionOrg(t, admin, "retention-suspended-old", "suspended", expired)

	if _, err := mod.SweepRetention(t.Context(), keepAll, sweepDays); err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}

	if !orgExists(t, admin, active) {
		t.Error("an active organization must never be purged, however old")
	}
	if !orgExists(t, admin, suspended) {
		t.Error("a suspended organization must never be purged, however old")
	}
}

func TestSweepRetention_PurgeCascadesInsideOrganizationOnly(t *testing.T) {
	admin, mod := orgWorkerModule(t)
	ctx := t.Context()
	org := seedRetentionOrg(t, admin, "retention-cascade", "deleted", expired)

	if _, err := admin.Exec(ctx, `
		INSERT INTO organization.memberships (organization_id, auth_sub, role) VALUES ($1, 'retention_owner', 'owner')`, org); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO organization.invitations (organization_id, email, token, invited_by, expires_at)
		VALUES ($1, 'retention@example.com', 'retention-cascade-token', 'retention_owner', now() + interval '7 days')`, org); err != nil {
		t.Fatalf("seed invitation: %v", err)
	}
	var endpoint string
	if err := admin.QueryRow(ctx, `
		INSERT INTO organization.webhook_endpoints (organization_id, url, secret_encrypted)
		VALUES ($1, 'https://example.com/hook', '\x00') RETURNING id`, org).Scan(&endpoint); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO organization.webhook_deliveries (endpoint_id, event_id, event_type) VALUES ($1, 'evt', 'test.event')`, endpoint); err != nil {
		t.Fatalf("seed delivery: %v", err)
	}

	// Rows in other schemas reference the organization by plain value, with no
	// foreign key, so a purge leaves them for their own retention rules.
	if _, err := admin.Exec(ctx, `
		INSERT INTO audit.events (organization_id, actor, action, resource, status_code)
		VALUES ($1, 'retention_owner', 'POST', '/retention-cascade', 200)`, org); err != nil {
		t.Fatalf("seed audit event: %v", err)
	}
	var subscription string
	if err := admin.QueryRow(ctx, `
		INSERT INTO billing.subscriptions (subject_type, subject_id, status)
		VALUES ('organization', $1, 'cancelled')
		RETURNING id`, org).Scan(&subscription); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	t.Cleanup(func() {
		background := context.Background()
		admin.Exec(background, `DELETE FROM audit.events WHERE organization_id = $1`, org)
		admin.Exec(background, `DELETE FROM billing.subscriptions WHERE id = $1`, subscription)
	})

	if _, err := mod.SweepRetention(ctx, keepAll, sweepDays); err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}

	if orgExists(t, admin, org) {
		t.Fatal("organization should be purged")
	}
	for _, query := range []string{
		`SELECT count(*) FROM organization.memberships WHERE organization_id = $1`,
		`SELECT count(*) FROM organization.invitations WHERE organization_id = $1`,
		`SELECT count(*) FROM organization.webhook_endpoints WHERE organization_id = $1`,
	} {
		var n int
		if err := admin.QueryRow(ctx, query, org).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		if n != 0 {
			t.Errorf("%s: %d rows left, want 0", query, n)
		}
	}
	var deliveries int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM organization.webhook_deliveries WHERE endpoint_id = $1`, endpoint).Scan(&deliveries); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if deliveries != 0 {
		t.Errorf("%d deliveries left, want 0", deliveries)
	}

	var auditRows, billingRows int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM audit.events WHERE organization_id = $1`, org).Scan(&auditRows); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM billing.subscriptions WHERE id = $1`, subscription).Scan(&billingRows); err != nil {
		t.Fatalf("count billing: %v", err)
	}
	if auditRows != 1 || billingRows != 1 {
		t.Errorf("audit rows = %d, billing rows = %d, want both kept", auditRows, billingRows)
	}
}

func TestSweepRetention_PurgeDrainsPastOneBatch(t *testing.T) {
	admin, mod := orgWorkerModule(t)
	ctx := t.Context()
	cleanup := func() {
		admin.Exec(context.Background(), `DELETE FROM organization.organizations WHERE slug LIKE 'retention-batch-%'`)
	}
	cleanup()
	t.Cleanup(cleanup)

	if _, err := admin.Exec(ctx, `
		INSERT INTO organization.organizations (slug, name, owner_id, status, updated_at)
		SELECT 'retention-batch-' || g, 'batch', 'retention_owner', 'deleted', now() - make_interval(days => $1) - make_interval(secs => g)
		FROM generate_series(1, 10001) AS g`, expired); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := mod.SweepRetention(ctx, keepAll, sweepDays); err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}

	var left int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM organization.organizations WHERE slug LIKE 'retention-batch-%'`).Scan(&left); err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 0 {
		t.Errorf("%d left, want one sweep to drain more than one batch", left)
	}
}

func TestSweepRetention_WebhookDeliveries(t *testing.T) {
	admin, mod := orgWorkerModule(t)
	ctx := t.Context()
	org := seedRetentionOrg(t, admin, "retention-deliveries", "active", 0)
	var endpoint string
	if err := admin.QueryRow(ctx, `
		INSERT INTO organization.webhook_endpoints (organization_id, url, secret_encrypted)
		VALUES ($1, 'https://example.com/hook', '\x00') RETURNING id`, org).Scan(&endpoint); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}

	for _, age := range []int{expired, current} {
		if _, err := admin.Exec(ctx, `
			INSERT INTO organization.webhook_deliveries (endpoint_id, event_id, event_type, created_at)
			VALUES ($1, $2, 'test.event', now() - make_interval(days => $3))`, endpoint, strconv.Itoa(age), age); err != nil {
			t.Fatalf("seed delivery: %v", err)
		}
	}

	if _, err := mod.SweepRetention(ctx, sweepDays, keepAll); err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}

	var left string
	var n int
	if err := admin.QueryRow(ctx, `
		SELECT count(*), COALESCE(max(event_id), '') FROM organization.webhook_deliveries WHERE endpoint_id = $1`,
		endpoint).Scan(&n, &left); err != nil {
		t.Fatalf("read survivors: %v", err)
	}
	if n != 1 || left != "36499" {
		t.Errorf("want only the current delivery left, got %d rows, event %q", n, left)
	}

	// More than one batch on the same table.
	if _, err := admin.Exec(ctx, `DELETE FROM organization.webhook_deliveries WHERE endpoint_id = $1`, endpoint); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO organization.webhook_deliveries (endpoint_id, event_id, event_type, created_at)
		SELECT $1, g::text, 'test.event', now() - make_interval(days => $2) - make_interval(secs => g)
		FROM generate_series(1, 10001) AS g`, endpoint, expired); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	if _, err := mod.SweepRetention(ctx, sweepDays, keepAll); err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM organization.webhook_deliveries WHERE endpoint_id = $1`, endpoint).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("%d left, want one sweep to drain more than one batch", n)
	}
}
