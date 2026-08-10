package billing

// Integration tests for cancelSubscription's active-subscription scheduling
// branch and undoScheduledCancellation. Internal package, same reasoning as
// the other amendment test files in this package: the "undo" case has no
// HTTP route wired to it yet, and the concurrency test below needs the same
// real per-request transaction the RLS middleware normally provides.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/db"
)

// wantApperrCode asserts err is an *apperr.Error carrying the given Code.
// Every "wrong state to undo/schedule an amendment" condition in this
// package's amendment services is classified inline via apperr.Validation
// (no cause), so errors.Is against a raw sentinel doesn't resolve past that
// point — the classified Code is the stable, checkable identity instead,
// shared across the downgrade/addon/cancellation amendment test files.
func wantApperrCode(t *testing.T, err error, code string) {
	t.Helper()
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("want apperr %s, got %v", code, err)
	}
}

// TestIntegration_CancelSubscription_NonOrganizationSubject_NotFound exercises
// subjectType with a value other than "organization" — every real call site
// passes "organization" today, but the column and this function's parameter
// are polymorphic by design (see subjectTypeOrganization's own doc comment),
// so a lookup for a different subject type is expected to behave like any
// other lookup for a subscription that doesn't exist, not be rejected outright.
func TestIntegration_CancelSubscription_NonOrganizationSubject_NotFound(t *testing.T) {
	pool := testPoolAmendment(t)
	mod := NewModuleForTest(pool, nil)

	_, err := mod.svc.cancelSubscription(t.Context(), "user", "does-not-exist", "sub_owner", "too_expensive", "")
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("want a not-found error for a subject with no matching subscription, got %v", err)
	}
}

// TestIntegration_UndoScheduledCancellation_NonOrganizationSubject_NotFound
// is the same exercise for undoScheduledCancellation's subjectType parameter.
func TestIntegration_UndoScheduledCancellation_NonOrganizationSubject_NotFound(t *testing.T) {
	pool := testPoolAmendment(t)
	mod := NewModuleForTest(pool, nil)

	_, err := mod.svc.undoScheduledCancellation(t.Context(), "user", "does-not-exist", "sub_owner")
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("want a not-found error for a subject with no matching subscription, got %v", err)
	}
}

func TestIntegration_CancelSubscription_Active_Schedules(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b1d"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)

	updated, err := mod.svc.cancelSubscription(t.Context(), "organization", orgID, "sub_owner", "too_expensive", "")
	if err != nil {
		t.Fatalf("cancelSubscription: %v", err)
	}
	if updated.Status != statusActive {
		t.Errorf("want status unchanged (active) immediately after scheduling, got %q", updated.Status)
	}
	if updated.ScheduledCancelAt == nil {
		t.Fatal("want scheduled_cancel_at set on the returned record")
	}

	fresh, err := r.findSubscriptionByID(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("findSubscriptionByID: %v", err)
	}
	if fresh.Status != statusActive {
		t.Errorf("want live status unchanged in DB, got %q", fresh.Status)
	}
	if fresh.ScheduledCancelAt == nil {
		t.Error("want scheduled_cancel_at set in DB")
	}

	history, err := r.listHistory(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listHistory: %v", err)
	}
	if len(history) != 1 || history[0].Action != "cancel" {
		t.Fatalf("want exactly 1 cancel history row, got %+v", history)
	}

	var phase *string
	if err := pool.QueryRow(t.Context(), `SELECT phase FROM billing.subscription_history WHERE id = $1`, history[0].ID).Scan(&phase); err != nil {
		t.Fatalf("query phase: %v", err)
	}
	if phase == nil || *phase != historyPhaseScheduled {
		t.Errorf("want phase=scheduled on the history row, got %v", phase)
	}
}

func TestIntegration_CancelSubscription_Active_SupersedesOtherScheduledAmendments(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b1e"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 5)
	if err := r.schedulePlanDowngrade(t.Context(), pool, sub.ID, "growth", cycleYearly); err != nil {
		t.Fatalf("seed scheduled downgrade: %v", err)
	}
	if err := r.scheduleAddonQuantityChange(t.Context(), pool, sub.ID, "extra-seat", 2); err != nil {
		t.Fatalf("seed scheduled addon change: %v", err)
	}

	if _, err := mod.svc.cancelSubscription(t.Context(), "organization", orgID, "sub_owner", "too_expensive", ""); err != nil {
		t.Fatalf("cancelSubscription: %v", err)
	}

	fresh, err := r.findSubscriptionByID(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("findSubscriptionByID: %v", err)
	}
	if fresh.ScheduledPlan != nil || fresh.ScheduledCycle != nil {
		t.Errorf("want the scheduled plan downgrade superseded/cleared, got %v/%v", fresh.ScheduledPlan, fresh.ScheduledCycle)
	}
	if fresh.ScheduledCancelAt == nil {
		t.Error("want scheduled_cancel_at set")
	}

	addons, err := r.listAttachedAddonsWithPricing(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listAttachedAddonsWithPricing: %v", err)
	}
	if len(addons) != 1 || addons[0].ScheduledQuantity != nil {
		t.Errorf("want the scheduled addon change superseded/cleared, got %+v", addons)
	}
}

func TestIntegration_UndoScheduledCancellation_ClearsSchedule(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b1f"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
	if err := r.scheduleCancellation(t.Context(), pool, sub.ID); err != nil {
		t.Fatalf("scheduleCancellation: %v", err)
	}

	updated, err := mod.svc.undoScheduledCancellation(t.Context(), "organization", orgID, "sub_owner")
	if err != nil {
		t.Fatalf("undoScheduledCancellation: %v", err)
	}
	if updated.ScheduledCancelAt != nil {
		t.Errorf("want scheduled_cancel_at cleared, got %v", updated.ScheduledCancelAt)
	}
	if updated.Status != statusActive {
		t.Errorf("undo must never touch status, got %q", updated.Status)
	}
}

func TestIntegration_UndoScheduledCancellation_NothingScheduled(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b20"
	mod := NewModuleForTest(pool, nil)
	makeActiveAmendmentSubscription(t, pool, r, orgID)

	_, err := mod.svc.undoScheduledCancellation(t.Context(), "organization", orgID, "sub_owner")
	wantApperrCode(t, err, "NO_SCHEDULED_CANCELLATION")
}

func TestIntegration_UndoScheduledCancellation_AlreadyCancelled(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b21"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
	if err := r.scheduleCancellation(t.Context(), pool, sub.ID); err != nil {
		t.Fatalf("scheduleCancellation: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET status = 'cancelled' WHERE id = $1`, sub.ID); err != nil {
		t.Fatalf("force cancelled: %v", err)
	}

	_, err := mod.svc.undoScheduledCancellation(t.Context(), "organization", orgID, "sub_owner")
	wantApperrCode(t, err, "NO_SCHEDULED_CANCELLATION")
}

// TestIntegration_CancelSubscription_Concurrent_ScheduleAndUndo races
// cancelSubscription (scheduling) against undoScheduledCancellation
// (clearing) on the same subscription, each wrapped in its own real
// transaction the way the RLS middleware wraps a live request —
// reproducing lockSubscriptionForUpdate's actual serialization. Whichever
// write commits last decides the outcome; what must never happen is a torn
// write or either call erroring out.
func TestIntegration_CancelSubscription_Concurrent_ScheduleAndUndo(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b22"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
	if err := r.scheduleCancellation(t.Context(), pool, sub.ID); err != nil {
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
			_, err := mod.svc.cancelSubscription(ctx, "organization", orgID, "sub_owner", "too_expensive", "")
			return err
		})
	}()
	go func() {
		defer wg.Done()
		errs[1] = inTx(func(ctx context.Context) error {
			_, err := mod.svc.undoScheduledCancellation(ctx, "organization", orgID, "sub_owner")
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
	if fresh.Status != statusActive {
		t.Errorf("neither call touches status, want active, got %q", fresh.Status)
	}
}
