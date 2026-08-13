package organization

// Regression test for createWebhookEndpoint's billingReader.CheckFeatureAccess
// call with no ambient RLS transaction — the exact shape a real HTTP request
// hits it with, since organization's own routes carry no querier in context
// at all (organization schema has no RLS, so nothing ever installs one).
// Connects as the RLS-constrained app role (POSTGRES_APP_URL), not a
// superuser pool — checkFeatureAccess reads billing.subscriptions, which is
// FORCE ROW LEVEL SECURITY, so only a real RLS-constrained connection can
// prove a caller with no org context set actually gets one before that
// query runs.

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/platform/db"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

func testAppPoolWebhookGate(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_APP_URL")
	if dsn == "" {
		t.Skip("POSTGRES_APP_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// cleanupWebhookGateFixtures deletes this test's rows. billing.subscriptions
// is FORCE RLS, so its delete must run with org context set in the same
// transaction — a plain pool.Exec as the app role would silently affect 0
// rows and leave the fixture behind for the next run.
func cleanupWebhookGateFixtures(pool *pgxpool.Pool, orgID string) {
	ctx := context.Background()
	pool.Exec(ctx, `DELETE FROM organization.webhook_endpoints WHERE organization_id = $1`, orgID)
	pool.Exec(ctx, `DELETE FROM organization.organizations WHERE id = $1`, orgID)
	_ = db.WithTx(ctx, pool, func(tx db.Querier) error {
		if err := db.SetOrgContext(ctx, tx, orgID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1`, orgID)
		return err
	})
}

// TestCreateWebhookEndpoint_FeatureGate_NoAmbientTx verifies webhook
// creation succeeds with no ambient billing transaction: a real, active
// Growth-plan subscription (whose catalog entry lists "webhooks" —
// db/migrations/000004_billing.up.sql) must not get
// ErrWebhookFeatureNotAvailable just because checkFeatureAccess's own
// lookup runs with no org context set.
func TestCreateWebhookEndpoint_FeatureGate_NoAmbientTx(t *testing.T) {
	pool := testAppPoolWebhookGate(t)
	const orgID = "00000000-0000-0000-0000-0000000c1001"
	cleanupWebhookGateFixtures(pool, orgID)
	t.Cleanup(func() { cleanupWebhookGateFixtures(pool, orgID) })

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization.organizations (id, name, slug, owner_id) VALUES ($1, $2, $3, $4)`,
		orgID, "Webhook Gate Org", "webhook-gate-org", "webhook_gate_owner",
	); err != nil {
		t.Fatalf("seed organization: %v", err)
	}

	// Seed the subscription the way the interactive flow does: inside a
	// transaction with org context set, via the same RLS-constrained role
	// checkFeatureAccess itself will run as.
	periodStart := time.Now()
	periodEnd := periodStart.AddDate(0, 1, 0)
	if err := db.WithTx(ctx, pool, func(tx db.Querier) error {
		if err := db.SetOrgContext(ctx, tx, orgID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO billing.subscriptions (subject_type, subject_id, plan, status, currency, period_start, period_end)
			VALUES ('organization', $1, 'growth', 'active', 'USD', $2, $3)`,
			orgID, periodStart, periodEnd)
		return err
	}); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	billingMod := billing.New(pool, messaging.NoopPublisher{}, billing.ProviderConfig{}, nil, nil, nil)

	AllowLoopbackWebhooksForTest() // relax the https/public-IP SSRF guard for this test binary
	mod := NewModuleForTest(pool)
	mod.SetBillingReader(billingMod)

	// No querier in context at all — matches the real HTTP-handler shape:
	// organization's own routes are never wrapped in an RLS transaction
	// (organization schema has no RLS), so this is exactly what a live
	// request's context looks like when it reaches createWebhookEndpoint.
	_, _, err := mod.svc.createWebhookEndpoint(ctx, orgID, "http://127.0.0.1:1/hook", nil)
	if err != nil {
		if errors.Is(err, ErrWebhookFeatureNotAvailable) {
			t.Fatalf("createWebhookEndpoint failed with FEATURE_NOT_AVAILABLE — checkFeatureAccess was blocked by FORCE RLS with no org context set: %v", err)
		}
		t.Fatalf("createWebhookEndpoint: %v", err)
	}
}
