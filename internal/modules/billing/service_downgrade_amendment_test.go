package billing

// Integration tests for downgradeSubscription's active-subscription
// scheduling branch and undoScheduledPlanDowngrade. Internal package, same
// reasoning as repository_amendment_test.go: these service methods have no
// HTTP route wired to them for the "undo" case, and the concurrency test
// below needs to reproduce the real per-request transaction the RLS
// middleware normally provides (see rls.go) without going through gin.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/db"
)

// TestUnit_DowngradeSubscription_NonOrganizationSubject_Rejected exercises
// the subjectType guard directly — no DB access happens before it runs, so
// no pool/infra is needed. Every real caller passes "organization"; this
// path exists for a future non-organization subject, per
// subjectTypeOrganization's own comment.
func TestUnit_DowngradeSubscription_NonOrganizationSubject_Rejected(t *testing.T) {
	svc := &service{repo: &repository{}}
	_, _, err := svc.downgradeSubscription(t.Context(), "user", "u1", "solo", cycleMonthly, "sub_owner", nil, nil)
	if err == nil {
		t.Fatal("want an error for a non-organization subjectType, got nil")
	}
}

// TestIntegration_UndoScheduledPlanDowngrade_NonOrganizationSubject_NotFound
// exercises undoScheduledPlanDowngrade's subjectType with a value other than
// "organization" — unlike downgradeSubscription, this method has no explicit
// reject-guard (subjectType is a genuine polymorphic parameter by design, see
// subjectTypeOrganization's own doc comment); a lookup for a different
// subject type behaves like any other lookup for a subscription that doesn't
// exist, not a rejection.
func TestIntegration_UndoScheduledPlanDowngrade_NonOrganizationSubject_NotFound(t *testing.T) {
	pool := testPoolAmendment(t)
	mod := NewModuleForTest(pool, nil)

	_, err := mod.svc.undoScheduledPlanDowngrade(t.Context(), "user", "does-not-exist")
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("want a not-found error for a subject with no matching subscription, got %v", err)
	}
}

func TestIntegration_DowngradeSubscription_Active_SchedulesInsteadOfApplying(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b0e"
	mod := NewModuleForTest(pool, nil)
	cleanupAmendmentSubscription(pool, orgID)
	t.Cleanup(func() { cleanupAmendmentSubscription(pool, orgID) })
	sub, err := r.insertSubscription(t.Context(), pool, "organization", orgID, "growth", cycleMonthly, "USD")
	if err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	updated, overage, err := mod.svc.downgradeSubscription(t.Context(), "organization", orgID, "solo", cycleMonthly, "sub_owner", nil, nil)
	if err != nil {
		t.Fatalf("downgradeSubscription: %v", err)
	}
	if len(overage.RemovedMemberAuthSubs) != 0 || len(overage.AutoSelectedMemberSubs) != 0 ||
		len(overage.RemovedFileIDs) != 0 || len(overage.AutoSelectedFileIDs) != 0 {
		t.Errorf("want zero-value OverageResolution for the scheduled case, got %+v", overage)
	}
	if updated.Plan != "growth" || updated.Cycle != cycleMonthly {
		t.Errorf("live plan/cycle must stay unchanged immediately after scheduling, got %s/%s", updated.Plan, updated.Cycle)
	}
	if updated.ScheduledPlan == nil || *updated.ScheduledPlan != "solo" {
		t.Errorf("want scheduled_plan=solo on the returned record, got %v", updated.ScheduledPlan)
	}
	if updated.ScheduledCycle == nil || *updated.ScheduledCycle != cycleMonthly {
		t.Errorf("want scheduled_cycle=monthly on the returned record, got %v", updated.ScheduledCycle)
	}

	fresh, err := r.findSubscriptionByID(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("findSubscriptionByID: %v", err)
	}
	if fresh.Plan != "growth" {
		t.Errorf("want live plan unchanged in DB, got %q", fresh.Plan)
	}
	if fresh.ScheduledPlan == nil || *fresh.ScheduledPlan != "solo" {
		t.Errorf("want scheduled_plan=solo in DB, got %v", fresh.ScheduledPlan)
	}

	history, err := r.listHistory(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("want exactly 1 history row, got %d", len(history))
	}
	if history[0].Action != "downgrade" || history[0].AmountCents != 0 {
		t.Errorf("want action=downgrade amount_cents=0, got action=%q amount_cents=%d", history[0].Action, history[0].AmountCents)
	}

	var phase *string
	if err := pool.QueryRow(t.Context(), `SELECT phase FROM billing.subscription_history WHERE id = $1`, history[0].ID).Scan(&phase); err != nil {
		t.Fatalf("query phase: %v", err)
	}
	if phase == nil || *phase != historyPhaseScheduled {
		t.Errorf("want phase=scheduled on the history row, got %v", phase)
	}
}

func TestIntegration_DowngradeSubscription_Active_CancellationScheduled_Rejected(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b0f"
	mod := NewModuleForTest(pool, nil)
	sub := seedAmendmentSubscription(t, pool, r, orgID)
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET plan = 'growth' WHERE id = $1`, sub.ID); err != nil {
		t.Fatalf("seed growth plan: %v", err)
	}
	if err := r.scheduleCancellation(t.Context(), pool, sub.ID); err != nil {
		t.Fatalf("scheduleCancellation: %v", err)
	}

	_, _, err := mod.svc.downgradeSubscription(t.Context(), "organization", orgID, "solo", cycleMonthly, "sub_owner", nil, nil)
	wantApperrCode(t, err, cancellationScheduledCode)

	fresh, err := r.findSubscriptionByID(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("findSubscriptionByID: %v", err)
	}
	if fresh.ScheduledPlan != nil {
		t.Errorf("want nothing written when rejected, got scheduled_plan=%v", fresh.ScheduledPlan)
	}
	history, err := r.listHistory(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listHistory: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("want no history row written when rejected, got %d", len(history))
	}
}

func TestIntegration_UndoScheduledPlanDowngrade_ClearsSchedule(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b10"
	mod := NewModuleForTest(pool, nil)
	sub := seedAmendmentSubscription(t, pool, r, orgID)
	if err := r.schedulePlanDowngrade(t.Context(), pool, sub.ID, "growth", cycleYearly); err != nil {
		t.Fatalf("schedulePlanDowngrade: %v", err)
	}

	updated, err := mod.svc.undoScheduledPlanDowngrade(t.Context(), "organization", orgID)
	if err != nil {
		t.Fatalf("undoScheduledPlanDowngrade: %v", err)
	}
	if updated.ScheduledPlan != nil || updated.ScheduledCycle != nil || updated.ScheduledRequestedAt != nil {
		t.Errorf("want every scheduled column cleared, got %+v", updated)
	}
	if updated.Plan != "solo" {
		t.Errorf("undo must never touch the live plan, got %q", updated.Plan)
	}
}

func TestIntegration_UndoScheduledPlanDowngrade_NothingScheduled(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b11"
	mod := NewModuleForTest(pool, nil)
	seedAmendmentSubscription(t, pool, r, orgID)

	_, err := mod.svc.undoScheduledPlanDowngrade(t.Context(), "organization", orgID)
	wantApperrCode(t, err, "NO_SCHEDULED_DOWNGRADE")
}

// TestIntegration_DowngradeSubscription_Concurrent_ScheduleAndUndo races
// downgradeSubscription (re-scheduling to a different plan) against
// undoScheduledPlanDowngrade (clearing the existing schedule) on the same
// subscription, each wrapped in its own real transaction the way the RLS
// middleware wraps a live request — reproducing lockSubscriptionForUpdate's
// actual serialization rather than the no-op it'd be against a bare pool
// connection. Whichever write commits last decides the outcome; both are
// valid (either something ends up scheduled, or nothing does) — what must
// never happen is a torn write or either call erroring out.
func TestIntegration_DowngradeSubscription_Concurrent_ScheduleAndUndo(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b12"
	mod := NewModuleForTest(pool, nil)
	sub := seedAmendmentSubscription(t, pool, r, orgID)
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET plan = 'growth' WHERE id = $1`, sub.ID); err != nil {
		t.Fatalf("seed growth plan: %v", err)
	}
	if err := r.schedulePlanDowngrade(t.Context(), pool, sub.ID, "solo", cycleMonthly); err != nil {
		t.Fatalf("seed initial schedule: %v", err)
	}

	inTx := func(fn func(ctx context.Context) error) error {
		ctx := context.Background()
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if err := fn(db.WithQuerier(ctx, tx)); err != nil {
			tx.Rollback(ctx)
			return err
		}
		return tx.Commit(ctx)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs[0] = inTx(func(ctx context.Context) error {
			_, _, err := mod.svc.downgradeSubscription(ctx, "organization", orgID, "solo", cycleMonthly, "sub_owner", nil, nil)
			return err
		})
	}()
	go func() {
		defer wg.Done()
		errs[1] = inTx(func(ctx context.Context) error {
			_, err := mod.svc.undoScheduledPlanDowngrade(ctx, "organization", orgID)
			return err
		})
	}()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d: want no error, got %v", i, err)
		}
	}

	fresh, err := r.findSubscriptionByID(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("findSubscriptionByID: %v", err)
	}
	scheduledTogether := fresh.ScheduledPlan != nil && fresh.ScheduledCycle != nil && fresh.ScheduledRequestedAt != nil
	clearedTogether := fresh.ScheduledPlan == nil && fresh.ScheduledCycle == nil && fresh.ScheduledRequestedAt == nil
	if !scheduledTogether && !clearedTogether {
		t.Errorf("want a fully-scheduled or fully-cleared row, never a torn write: %+v", fresh)
	}
	if scheduledTogether && *fresh.ScheduledPlan != "solo" {
		t.Errorf("if something ended up scheduled it must be downgradeSubscription's re-write (solo), got %q", *fresh.ScheduledPlan)
	}
}
