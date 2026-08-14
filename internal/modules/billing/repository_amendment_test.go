package billing

// Integration tests for the scheduled-amendment repository functions.
// Unlike pure_test.go these hit a real Postgres instance, and unlike
// integration_test.go (billing_test, HTTP-driven) they call unexported
// repository methods directly — nothing outside this package calls them
// yet, so there's no HTTP path to exercise them through.

import (
	"context"
	"errors"
	"maps"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testPoolAmendment seeds billing.subscriptions directly and some tests in this file-set create
// throwaway trigger functions to inject a mid-transaction failure — FORCE ROW LEVEL SECURITY needs
// BYPASSRLS for the former, CREATE on the billing schema needs schema ownership for the latter.
// TEST_DATABASE_URL (stratum_test, see deploy/postgres-init/02-test-role.sql) holds both, by
// design rather than by container-superuser accident — see docs/09-testing.md.
func testPoolAmendment(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// cleanupAmendmentSubscription tears down every row a subscription in this
// test file-set could plausibly have accumulated, including invoices/line
// items/payment links — earlier versions of this helper predated any test
// here invoking the real renewal worker, which creates an invoice row; left
// out, that invoice's FK blocks the subscriptions DELETE below, pool.Exec
// swallows the resulting error silently, and the next run's insertSubscription
// then silently no-ops (ON CONFLICT DO NOTHING) against the leftover row.
func cleanupAmendmentSubscription(pool *pgxpool.Pool, orgID string) {
	ctx := context.Background()
	pool.Exec(ctx, `DELETE FROM billing.invoice_line_items WHERE invoice_id IN (SELECT i.id FROM billing.invoices i JOIN billing.subscriptions s ON s.id = i.subscription_id WHERE s.subject_type = 'organization' AND s.subject_id = $1)`, orgID)
	pool.Exec(ctx, `DELETE FROM billing.payment_links WHERE invoice_id IN (SELECT i.id FROM billing.invoices i JOIN billing.subscriptions s ON s.id = i.subscription_id WHERE s.subject_type = 'organization' AND s.subject_id = $1)`, orgID)
	pool.Exec(ctx, `DELETE FROM billing.invoices WHERE subscription_id IN (SELECT id FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1)`, orgID)
	pool.Exec(ctx, `DELETE FROM billing.subscription_history WHERE subscription_id IN (SELECT id FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1)`, orgID)
	pool.Exec(ctx, `DELETE FROM billing.subscription_addons WHERE subscription_id IN (SELECT id FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1)`, orgID)
	pool.Exec(ctx, `DELETE FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1`, orgID)
}

// seedAmendmentSubscription inserts a fresh active "solo" subscription for
// orgID and registers cleanup before and after the test.
func seedAmendmentSubscription(t *testing.T, pool *pgxpool.Pool, r *repository, orgID string) *subscriptionRecord {
	t.Helper()
	cleanupAmendmentSubscription(pool, orgID)
	t.Cleanup(func() { cleanupAmendmentSubscription(pool, orgID) })
	s, err := r.insertSubscription(t.Context(), pool, "organization", orgID, "solo", cycleMonthly, "USD")
	if err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	return s
}

func TestIntegration_SchedulePlanDowngrade_RoundTrip(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b04"
	sub := seedAmendmentSubscription(t, pool, r, orgID)

	// fresh subscription reads back with every scheduled column nil
	fresh, err := r.findSubscriptionByID(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("findSubscriptionByID: %v", err)
	}
	if fresh.ScheduledPlan != nil || fresh.ScheduledCycle != nil || fresh.ScheduledRequestedAt != nil || fresh.ScheduledCancelAt != nil {
		t.Fatalf("want all scheduled columns nil on fresh subscription, got %+v", fresh)
	}

	if err := r.schedulePlanDowngrade(t.Context(), pool, sub.ID, "growth", cycleYearly); err != nil {
		t.Fatalf("schedulePlanDowngrade: %v", err)
	}
	scheduled, err := r.findSubscriptionByID(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("findSubscriptionByID after schedule: %v", err)
	}
	if scheduled.ScheduledPlan == nil || *scheduled.ScheduledPlan != "growth" {
		t.Errorf("want scheduled_plan=growth, got %v", scheduled.ScheduledPlan)
	}
	if scheduled.ScheduledCycle == nil || *scheduled.ScheduledCycle != cycleYearly {
		t.Errorf("want scheduled_cycle=yearly, got %v", scheduled.ScheduledCycle)
	}
	if scheduled.ScheduledRequestedAt == nil {
		t.Error("want scheduled_requested_at set, got nil")
	}
	if scheduled.Plan != "solo" {
		t.Errorf("live plan must stay unchanged until applied, got %q", scheduled.Plan)
	}

	if err := r.clearScheduledPlanDowngrade(t.Context(), pool, sub.ID); err != nil {
		t.Fatalf("clearScheduledPlanDowngrade: %v", err)
	}
	cleared, err := r.findSubscriptionByID(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("findSubscriptionByID after clear: %v", err)
	}
	if cleared.ScheduledPlan != nil || cleared.ScheduledCycle != nil || cleared.ScheduledRequestedAt != nil {
		t.Errorf("want scheduled columns nil after undo, got %+v", cleared)
	}
	if cleared.Plan != "solo" {
		t.Errorf("clearing a schedule must never touch the live plan, got %q", cleared.Plan)
	}
}

func TestIntegration_ApplyScheduledPlanDowngrade_NoneScheduledIsErrNoRows(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b05"
	sub := seedAmendmentSubscription(t, pool, r, orgID)

	_, err := r.applyScheduledPlanDowngrade(t.Context(), pool, sub.ID)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("want pgx.ErrNoRows wrapped, got %v", err)
	}
}

func TestIntegration_ApplyScheduledPlanDowngrade_MovesLiveAndClearsSchedule(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b06"
	sub := seedAmendmentSubscription(t, pool, r, orgID)

	if err := r.schedulePlanDowngrade(t.Context(), pool, sub.ID, "growth", cycleYearly); err != nil {
		t.Fatalf("schedulePlanDowngrade: %v", err)
	}

	applied, err := r.applyScheduledPlanDowngrade(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("applyScheduledPlanDowngrade: %v", err)
	}
	if applied.Plan != "growth" || applied.Cycle != cycleYearly {
		t.Errorf("want live plan/cycle=growth/yearly, got %s/%s", applied.Plan, applied.Cycle)
	}
	if applied.ScheduledPlan != nil || applied.ScheduledCycle != nil || applied.ScheduledRequestedAt != nil {
		t.Errorf("want schedule cleared after apply, got %+v", applied)
	}

	// applying again with nothing left scheduled is a no-op error, not a
	// silent success against the just-applied plan.
	if _, err := r.applyScheduledPlanDowngrade(t.Context(), pool, sub.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("want pgx.ErrNoRows on re-apply, got %v", err)
	}
}

func TestIntegration_ScheduleCancellation_RoundTrip(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b07"
	sub := seedAmendmentSubscription(t, pool, r, orgID)

	if err := r.scheduleCancellation(t.Context(), pool, sub.ID); err != nil {
		t.Fatalf("scheduleCancellation: %v", err)
	}
	scheduled, err := r.findSubscriptionByID(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("findSubscriptionByID: %v", err)
	}
	if scheduled.ScheduledCancelAt == nil {
		t.Fatal("want scheduled_cancel_at set")
	}
	if scheduled.Status != statusActive {
		t.Errorf("scheduling a cancellation must not change status yet, got %q", scheduled.Status)
	}

	if err := r.clearScheduledCancellation(t.Context(), pool, sub.ID); err != nil {
		t.Fatalf("clearScheduledCancellation: %v", err)
	}
	cleared, err := r.findSubscriptionByID(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("findSubscriptionByID after clear: %v", err)
	}
	if cleared.ScheduledCancelAt != nil {
		t.Errorf("want scheduled_cancel_at nil after undo, got %v", cleared.ScheduledCancelAt)
	}
}

// TestIntegration_SubscriptionScheduledCheckConstraint confirms the
// composite CHECK constraint still rejects a partial write even when issued
// directly (i.e. not through the all-or-nothing repo functions above) — the
// invariant lives in the database, not just in these functions' own
// discipline.
func TestIntegration_SubscriptionScheduledCheckConstraint(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b08"
	sub := seedAmendmentSubscription(t, pool, r, orgID)

	_, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET scheduled_plan = $2 WHERE id = $1`, sub.ID, "growth")
	if err == nil {
		t.Fatal("want CHECK violation setting scheduled_plan without scheduled_cycle/scheduled_requested_at, got nil error")
	}
}

func seedAmendmentAddon(t *testing.T, pool *pgxpool.Pool, r *repository, subscriptionID, addonID string, quantity int) {
	t.Helper()
	if err := r.upsertSubscriptionAddon(t.Context(), pool, subscriptionID, addonID, quantity); err != nil {
		t.Fatalf("seed addon %s: %v", addonID, err)
	}
}

// testAddonID is a second, independently-tracked addon several amendment
// tests need to prove per-addon bookkeeping doesn't cross-contaminate — the
// real catalog only has one addon (extra-seat). Mapped to the "workspaces"
// feature (metered, unrelated to members) rather than a made-up feature, so
// tests asserting independent per-metric deltas still exercise a real,
// distinct metric.
const testAddonID = "extra-workspace"

// seedTestAddonCatalogRow inserts testAddonID into the billing catalog for
// tests that need a second real addon ID, and cleans it up afterward. Must
// be called before seedAmendmentSubscription so t.Cleanup's LIFO order runs
// the subscription/subscription_addons cleanup first, clearing the FK
// reference before this row is deleted.
func seedTestAddonCatalogRow(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `
		INSERT INTO billing.addons (id, name, description, prices) VALUES
		($1, 'Test +1 Workspace', 'Test-only addon for integration tests.',
		 '{"USD": {"monthly": 100, "yearly": 1000}, "IDR": {"monthly": 10000, "yearly": 100000}}')
		ON CONFLICT (id) DO NOTHING`, testAddonID); err != nil {
		t.Fatalf("seed test addon catalog row: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO billing.addon_features (addon_id, feature_id, limit_value) VALUES ($1, 'workspaces', 1)
		ON CONFLICT (addon_id, feature_id) DO NOTHING`, testAddonID); err != nil {
		t.Fatalf("seed test addon_features row: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM billing.addon_features WHERE addon_id = $1`, testAddonID)
		pool.Exec(context.Background(), `DELETE FROM billing.addons WHERE id = $1`, testAddonID)
	})
}

func TestIntegration_ScheduleAddonQuantityChange_RoundTrip(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b09"
	sub := seedAmendmentSubscription(t, pool, r, orgID)
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 3)

	rows, err := r.listScheduledAddonChanges(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listScheduledAddonChanges before scheduling: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("want empty slice when nothing scheduled, got %d rows", len(rows))
	}

	if err := r.scheduleAddonQuantityChange(t.Context(), pool, sub.ID, "extra-seat", 1); err != nil {
		t.Fatalf("scheduleAddonQuantityChange: %v", err)
	}

	addons, err := r.listAttachedAddonsWithPricing(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listAttachedAddonsWithPricing: %v", err)
	}
	if len(addons) != 1 {
		t.Fatalf("want 1 attached addon, got %d", len(addons))
	}
	if addons[0].Quantity != 3 {
		t.Errorf("live quantity must stay unchanged until applied, got %d", addons[0].Quantity)
	}
	if addons[0].ScheduledQuantity == nil || *addons[0].ScheduledQuantity != 1 {
		t.Errorf("want scheduled_quantity=1, got %v", addons[0].ScheduledQuantity)
	}
	if addons[0].ScheduledRequestedAt == nil {
		t.Error("want scheduled_requested_at set")
	}

	rows, err = r.listScheduledAddonChanges(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listScheduledAddonChanges after scheduling: %v", err)
	}
	if len(rows) != 1 || rows[0].AddonID != "extra-seat" || rows[0].LiveQuantity != 3 || rows[0].ScheduledQuantity != 1 {
		t.Errorf("want [{extra-seat 3 1}], got %+v", rows)
	}

	if err := r.clearScheduledAddonQuantityChange(t.Context(), pool, sub.ID, "extra-seat"); err != nil {
		t.Fatalf("clearScheduledAddonQuantityChange: %v", err)
	}
	addons, err = r.listAttachedAddonsWithPricing(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listAttachedAddonsWithPricing after clear: %v", err)
	}
	if addons[0].ScheduledQuantity != nil {
		t.Errorf("want scheduled_quantity nil after undo, got %v", addons[0].ScheduledQuantity)
	}
}

func TestIntegration_ClearAllScheduledAddonQuantityChanges(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b0a"
	seedTestAddonCatalogRow(t, pool)
	sub := seedAmendmentSubscription(t, pool, r, orgID)
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 2)
	seedAmendmentAddon(t, pool, r, sub.ID, testAddonID, 5)

	if err := r.scheduleAddonQuantityChange(t.Context(), pool, sub.ID, "extra-seat", 0); err != nil {
		t.Fatalf("schedule extra-seat: %v", err)
	}
	if err := r.scheduleAddonQuantityChange(t.Context(), pool, sub.ID, testAddonID, 3); err != nil {
		t.Fatalf("schedule %s: %v", testAddonID, err)
	}

	if err := r.clearAllScheduledAddonQuantityChanges(t.Context(), pool, sub.ID); err != nil {
		t.Fatalf("clearAllScheduledAddonQuantityChanges: %v", err)
	}

	rows, err := r.listScheduledAddonChanges(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listScheduledAddonChanges: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("want every scheduled addon change cleared, got %+v", rows)
	}
}

func TestIntegration_ApplyScheduledAddonQuantityChange(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b0b"
	sub := seedAmendmentSubscription(t, pool, r, orgID)
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 4)

	if err := r.scheduleAddonQuantityChange(t.Context(), pool, sub.ID, "extra-seat", 2); err != nil {
		t.Fatalf("scheduleAddonQuantityChange: %v", err)
	}
	if err := r.applyScheduledAddonQuantityChange(t.Context(), pool, sub.ID, "extra-seat", 2); err != nil {
		t.Fatalf("applyScheduledAddonQuantityChange: %v", err)
	}

	addons, err := r.listAttachedAddonsWithPricing(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listAttachedAddonsWithPricing: %v", err)
	}
	if len(addons) != 1 {
		t.Fatalf("want 1 attached addon, got %d", len(addons))
	}
	if addons[0].Quantity != 2 {
		t.Errorf("want live quantity=2 after apply, got %d", addons[0].Quantity)
	}
	if addons[0].ScheduledQuantity != nil {
		t.Errorf("want scheduled_quantity cleared after apply, got %v", addons[0].ScheduledQuantity)
	}
}

// TestIntegration_AddonScheduledCheckConstraint mirrors the subscriptions
// CHECK test above for billing.subscription_addons.
func TestIntegration_AddonScheduledCheckConstraint(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b0c"
	sub := seedAmendmentSubscription(t, pool, r, orgID)
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 1)

	_, err := pool.Exec(t.Context(),
		`UPDATE billing.subscription_addons SET scheduled_quantity = 0 WHERE subscription_id = $1 AND addon_id = $2`,
		sub.ID, "extra-seat")
	if err == nil {
		t.Fatal("want CHECK violation setting scheduled_quantity without scheduled_requested_at, got nil error")
	}
}

func TestIntegration_InsertHistoryWithPhase(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b0d"
	sub := seedAmendmentSubscription(t, pool, r, orgID)

	fromPlan, toPlan := "solo", "growth"
	phase := historyPhaseScheduled
	effectiveAt := time.Now().Add(30 * 24 * time.Hour)
	id, err := r.insertHistoryWithPhase(t.Context(), pool, sub.ID, "downgrade",
		&fromPlan, &toPlan, 0, "USD", "sub_owner", nil, &phase, &effectiveAt)
	if err != nil {
		t.Fatalf("insertHistoryWithPhase: %v", err)
	}
	if id == "" {
		t.Fatal("want non-empty history id")
	}

	var gotPhase *string
	var gotEffectiveAt *time.Time
	err = pool.QueryRow(t.Context(), `SELECT phase, effective_at FROM billing.subscription_history WHERE id = $1`, id).
		Scan(&gotPhase, &gotEffectiveAt)
	if err != nil {
		t.Fatalf("query back: %v", err)
	}
	if gotPhase == nil || *gotPhase != historyPhaseScheduled {
		t.Errorf("want phase=scheduled, got %v", gotPhase)
	}
	if gotEffectiveAt == nil {
		t.Error("want effective_at set")
	}

	// insertHistory (existing call sites) must still leave phase/effective_at NULL.
	plainID, err := r.insertHistory(t.Context(), pool, sub.ID, "downgrade", &fromPlan, &toPlan, 0, "USD", "sub_owner", nil)
	if err != nil {
		t.Fatalf("insertHistory: %v", err)
	}
	err = pool.QueryRow(t.Context(), `SELECT phase, effective_at FROM billing.subscription_history WHERE id = $1`, plainID).
		Scan(&gotPhase, &gotEffectiveAt)
	if err != nil {
		t.Fatalf("query back plain insertHistory row: %v", err)
	}
	if gotPhase != nil || gotEffectiveAt != nil {
		t.Errorf("want insertHistory's row to leave phase/effective_at nil, got phase=%v effective_at=%v", gotPhase, gotEffectiveAt)
	}
}

// TestIntegration_FutureAddonLimitDeltas_EmptyOverrides_MatchesAddonLimitDeltas
// is the regression guard futureAddonLimitDeltas' own doc comment promises:
// with no scheduled quantity overrides, it must compute exactly what
// addonLimitDeltas computes — a true superset, not a divergent
// reimplementation of the same join.
func TestIntegration_FutureAddonLimitDeltas_EmptyOverrides_MatchesAddonLimitDeltas(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b10"
	seedTestAddonCatalogRow(t, pool)
	sub := seedAmendmentSubscription(t, pool, r, orgID)
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 3)
	seedAmendmentAddon(t, pool, r, sub.ID, testAddonID, 2)

	live, err := r.addonLimitDeltas(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("addonLimitDeltas: %v", err)
	}
	future, err := r.futureAddonLimitDeltas(t.Context(), pool, sub.ID, map[string]int{})
	if err != nil {
		t.Fatalf("futureAddonLimitDeltas: %v", err)
	}
	if !maps.Equal(live, future) {
		t.Errorf("want futureAddonLimitDeltas(overrides=empty) == addonLimitDeltas, got %+v vs %+v", future, live)
	}
}

// TestIntegration_FutureAddonLimitDeltas_OverrideAppliesInPlaceOfLiveQuantity
// covers the actual override behavior: an addon with a scheduled quantity
// contributes that scheduled quantity instead of its live one, while every
// other attached addon still contributes its live quantity.
func TestIntegration_FutureAddonLimitDeltas_OverrideAppliesInPlaceOfLiveQuantity(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b11"
	seedTestAddonCatalogRow(t, pool)
	sub := seedAmendmentSubscription(t, pool, r, orgID)
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 3) // members: +3 live
	seedAmendmentAddon(t, pool, r, sub.ID, testAddonID, 2)  // workspaces: +2 live, no override

	future, err := r.futureAddonLimitDeltas(t.Context(), pool, sub.ID, map[string]int{"extra-seat": 1})
	if err != nil {
		t.Fatalf("futureAddonLimitDeltas: %v", err)
	}
	if future["members"] != 1 {
		t.Errorf("want overridden addon to contribute its scheduled quantity (1), got members delta %d", future["members"])
	}
	if future["workspaces"] != 2 {
		t.Errorf("want non-overridden addon to still contribute its live quantity (2), got workspaces delta %d", future["workspaces"])
	}
}
