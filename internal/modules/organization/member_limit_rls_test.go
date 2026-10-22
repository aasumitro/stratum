package organization

// Regression test for createInvitation's billingReader.CheckUsageLimit call
// with no ambient RLS transaction — the exact shape a real HTTP request
// hits it with, since organization's own routes carry no querier in context
// at all (organization schema has no RLS, so nothing ever installs one).
// Connects as the RLS-constrained app role (POSTGRES_APP_URL) for the
// subscription/usage seed and the call under test, plus the migration
// superuser role (TEST_DATABASE_URL) for fixture setup/teardown and the
// report-only over-limit scan below, which needs to see every
// organization's subscription in one query rather than one org context at
// a time.

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/platform/db"
)

func testPoolsMemberLimit(t *testing.T) (testPool, appPool *pgxpool.Pool) {
	t.Helper()
	testDBURL := os.Getenv("TEST_DATABASE_URL")
	appURL := os.Getenv("POSTGRES_APP_URL")
	if testDBURL == "" || appURL == "" {
		t.Skip("TEST_DATABASE_URL and POSTGRES_APP_URL required for member limit RLS test")
	}
	ctx := context.Background()
	var err error
	testPool, err = pgxpool.New(ctx, testDBURL)
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(testPool.Close)
	appPool, err = pgxpool.New(ctx, appURL)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	t.Cleanup(appPool.Close)
	return testPool, appPool
}

// cleanupMemberLimitFixtures deletes this test's rows via testPool, the
// migration superuser role — it bypasses RLS entirely (including FORCE RLS
// on billing.subscriptions), same convention webhook_rls_test.go's own
// cleanupWebhookFixture already uses in the billing package.
func cleanupMemberLimitFixtures(testPool *pgxpool.Pool, orgID string) {
	ctx := context.Background()
	testPool.Exec(ctx, `DELETE FROM organization.invitations WHERE organization_id = $1`, orgID)
	testPool.Exec(ctx, `DELETE FROM billing.usage WHERE organization_id = $1`, orgID)
	testPool.Exec(ctx, `DELETE FROM organization.memberships WHERE organization_id = $1`, orgID)
	testPool.Exec(ctx, `DELETE FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1`, orgID)
	testPool.Exec(ctx, `DELETE FROM organization.organizations WHERE id = $1`, orgID)
}

// reportOrgsOverMemberLimit is report-only: newly-enforced seat limits could
// reject an organization that already has more active members than its plan
// allows, accumulated while enforcement was broken. Any such organization is
// a pre-existing data-state finding for a human to decide how to handle
// (grandfather or true-up), not something this test truncates or fails
// over. Reads via testPool (bypasses RLS) so every organization's
// subscription is visible in one query.
func reportOrgsOverMemberLimit(t *testing.T, testPool *pgxpool.Pool) {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `
		SELECT s.subject_id, s.plan, COUNT(m.id) AS member_count, pf.limit_value
		FROM billing.subscriptions s
		JOIN organization.organizations o ON o.id::text = s.subject_id
		LEFT JOIN organization.memberships m ON m.organization_id = o.id
		JOIN billing.plan_features pf ON pf.plan_id = s.plan AND pf.feature_id = 'members'
		WHERE s.subject_type = 'organization' AND pf.limit_value >= 0
		GROUP BY s.subject_id, s.plan, pf.limit_value
		HAVING COUNT(m.id) > pf.limit_value`)
	if err != nil {
		t.Logf("reportOrgsOverMemberLimit: query failed (non-fatal, report-only): %v", err)
		return
	}
	defer rows.Close()

	found := 0
	for rows.Next() {
		var orgID, plan string
		var count int64
		var limit int
		if scanErr := rows.Scan(&orgID, &plan, &count, &limit); scanErr != nil {
			t.Logf("reportOrgsOverMemberLimit: scan failed: %v", scanErr)
			continue
		}
		found++
		t.Logf("FINDING: organization %s is over its plan's member limit — plan=%s members=%d limit=%d (pre-existing data state, not modified by this test)",
			orgID, plan, count, limit)
	}
	if err := rows.Err(); err != nil {
		t.Logf("reportOrgsOverMemberLimit: row iteration failed: %v", err)
	}
	if found == 0 {
		t.Log("reportOrgsOverMemberLimit: no organization currently exceeds its plan's member limit")
	}
}

// TestCreateInvitation_MemberLimit_NoAmbientTx verifies createInvitation's
// seat-limit enforcement holds even with no ambient billing transaction: a
// real Solo-plan subscription already at its members limit (1) must not
// admit a second invitee just because CheckUsageLimit's own lookup runs
// with no org context set. service_invitation.go's own doc comment notes
// this check fails open on a lookup error, so a blocked lookup would
// otherwise silently mean "no limit to enforce."
func TestCreateInvitation_MemberLimit_NoAmbientTx(t *testing.T) {
	testPool, appPool := testPoolsMemberLimit(t)

	// Report-only, before this test's own fixture exists — scans real
	// seed/QAT organizations, not the synthetic one below.
	reportOrgsOverMemberLimit(t, testPool)

	const orgID = "00000000-0000-0000-0000-0000000c1101"
	const ownerSub = "member_limit_rls_owner"
	cleanupMemberLimitFixtures(testPool, orgID)
	t.Cleanup(func() { cleanupMemberLimitFixtures(testPool, orgID) })

	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO organization.organizations (id, name, slug, owner_id) VALUES ($1, $2, $3, $4)`,
		orgID, "Member Limit RLS Org", "member-limit-rls-org", ownerSub,
	); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	// The one existing member: the owner's own seat, mirroring
	// provisionSubscription's real seeding of both a membership row and a
	// matching billing.usage row on organization creation.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO organization.memberships (organization_id, auth_sub, role) VALUES ($1, $2, 'owner')`,
		orgID, ownerSub,
	); err != nil {
		t.Fatalf("seed owner membership: %v", err)
	}

	// Seed the subscription and its usage cache the way the interactive
	// flow does: inside a transaction with org context set, via the same
	// RLS-constrained role checkUsageLimit itself will run as.
	periodStart := time.Now()
	periodEnd := periodStart.AddDate(0, 1, 0)
	if err := db.WithTx(ctx, appPool, func(tx db.Querier) error {
		if err := db.SetOrgContext(ctx, tx, orgID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO billing.subscriptions (subject_type, subject_id, plan, status, currency, period_start, period_end)
			VALUES ('organization', $1, 'solo', 'active', 'USD', $2, $3)`,
			orgID, periodStart, periodEnd); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO billing.usage (organization_id, metric, value, period_start, period_end)
			VALUES ($1, 'members', 1, $2, $3)`,
			orgID, periodStart, periodEnd)
		return err
	}); err != nil {
		t.Fatalf("seed subscription/usage: %v", err)
	}

	billingMod := billing.New(appPool, billing.ProviderConfig{}, nil, nil, nil)

	mod := NewModuleForTest(appPool)
	mod.SetBillingReader(billingMod)

	// No querier in context at all — matches the real HTTP-handler shape:
	// organization's own routes are never wrapped in an RLS transaction
	// (organization schema has no RLS), so this is exactly what a live
	// request's context looks like when it reaches createInvitation.
	_, err := mod.svc.createInvitation(ctx, orgID, "second-member@example.com", "member", ownerSub)
	if err == nil {
		t.Fatal("createInvitation: want ErrPlanLimitReached, got nil — seat limit was not enforced (a lookup error was silently treated as \"no limit\")")
	}
	if !errors.Is(err, ErrPlanLimitReached) {
		t.Fatalf("createInvitation: want ErrPlanLimitReached, got: %v", err)
	}
}
