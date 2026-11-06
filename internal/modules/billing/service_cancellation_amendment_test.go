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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts/events"
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

	// cancelSubscription locks the subscription row via s.querier(ctx),
	// which resolves to whatever transaction is already in ctx (the RLS
	// middleware's, in a live request) — wrap the call in a real
	// transaction here, mirroring how the RLS-wrapped route actually calls it.
	var updated *subscriptionRecord
	err := db.WithTx(t.Context(), pool, func(tx db.Querier) error {
		var txErr error
		updated, txErr = mod.svc.cancelSubscription(db.WithQuerier(t.Context(), tx), "organization", orgID, "sub_owner", "too_expensive", "")
		return txErr
	})
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

	if err := db.WithTx(t.Context(), pool, func(tx db.Querier) error {
		_, txErr := mod.svc.cancelSubscription(db.WithQuerier(t.Context(), tx), "organization", orgID, "sub_owner", "too_expensive", "")
		return txErr
	}); err != nil {
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

	var updated *subscriptionRecord
	err := db.WithTx(t.Context(), pool, func(tx db.Querier) error {
		var txErr error
		updated, txErr = mod.svc.undoScheduledCancellation(db.WithQuerier(t.Context(), tx), "organization", orgID, "sub_owner")
		return txErr
	})
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

	err := db.WithTx(t.Context(), pool, func(tx db.Querier) error {
		_, txErr := mod.svc.undoScheduledCancellation(db.WithQuerier(t.Context(), tx), "organization", orgID, "sub_owner")
		return txErr
	})
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

	err := db.WithTx(t.Context(), pool, func(tx db.Querier) error {
		_, txErr := mod.svc.undoScheduledCancellation(db.WithQuerier(t.Context(), tx), "organization", orgID, "sub_owner")
		return txErr
	})
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

// inServiceTx runs fn on a real transaction, the way the RLS middleware wraps
// a live billing request.
func inServiceTx(t *testing.T, pool *pgxpool.Pool, fn func(ctx context.Context) error) error {
	t.Helper()
	return db.WithTx(t.Context(), pool, func(tx db.Querier) error {
		return fn(db.WithQuerier(t.Context(), tx))
	})
}

// outboxRows returns the outbox payloads for orgID under routingKey.
func outboxRows(t *testing.T, pool *pgxpool.Pool, routingKey, orgID string) [][]byte {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT payload FROM messaging.outbox WHERE routing_key = $1 AND payload->>'org_id' = $2`, routingKey, orgID)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan outbox: %v", err)
		}
		out = append(out, payload)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	return out
}

func invoiceStatus(t *testing.T, pool *pgxpool.Pool, invoiceID string) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, invoiceID).Scan(&status); err != nil {
		t.Fatalf("read invoice status: %v", err)
	}
	return status
}

// TestIntegration_CancelSubscription_VoidsRenewal_UndoReissues: cancelling
// after the renewal invoice was issued voids it and expires its link, so a
// cancelled owner is neither billed nor dunned for the period they gave up;
// undo queues the auto-invoice again, and delivering it issues a fresh
// renewal invoice.
func TestIntegration_CancelSubscription_VoidsRenewal_UndoReissues(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b25"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET period_end = now() + interval '2 days' WHERE id = $1`, sub.ID); err != nil {
		t.Fatalf("move period_end inside the renewal window: %v", err)
	}
	renewal, err := r.insertInvoice(t.Context(), pool, orgID, sub.ID, 900, 0, 0, "USD", invoiceKindSubscription, false, nil)
	if err != nil {
		t.Fatalf("seed renewal invoice: %v", err)
	}
	var linkID string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO billing.payment_links (invoice_id, provider, currency, amount_cents, external_id, url)
		VALUES ($1, 'stripe', 'USD', 900, 'cs_b25', 'https://pay.example.com') RETURNING id`,
		renewal.ID).Scan(&linkID); err != nil {
		t.Fatalf("seed payment link: %v", err)
	}

	if err := inServiceTx(t, pool, func(ctx context.Context) error {
		_, err := mod.svc.cancelSubscription(ctx, subjectTypeOrganization, orgID, "sub_owner", "too_expensive", "")
		return err
	}); err != nil {
		t.Fatalf("cancelSubscription: %v", err)
	}
	if got := invoiceStatus(t, pool, renewal.ID); got != "void" {
		t.Errorf("after cancel: want the renewal invoice void, got %q", got)
	}
	var linkStatus string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.payment_links WHERE id = $1`, linkID).Scan(&linkStatus)
	if linkStatus != "expired" {
		t.Errorf("after cancel: want the renewal's payment link expired, got %q", linkStatus)
	}

	if _, err := pool.Exec(t.Context(), `DELETE FROM messaging.outbox WHERE payload->>'org_id' = $1`, orgID); err != nil {
		t.Fatalf("clear outbox: %v", err)
	}
	if err := inServiceTx(t, pool, func(ctx context.Context) error {
		_, err := mod.svc.undoScheduledCancellation(ctx, subjectTypeOrganization, orgID, "sub_owner")
		return err
	}); err != nil {
		t.Fatalf("undoScheduledCancellation: %v", err)
	}
	queued := outboxRows(t, pool, events.RoutingKeySubscriptionAutoInvoice, orgID)
	if len(queued) != 1 {
		t.Fatalf("after undo: want exactly one auto-invoice queued, got %d", len(queued))
	}

	// createPaymentLink fails with an empty ProviderConfig, after the
	// invoice itself has committed — the invoice is what this asserts.
	_ = mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), queued[0])
	var pendingRenewals int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.invoices WHERE subscription_id = $1 AND kind = 'subscription' AND status = 'pending'`,
		sub.ID).Scan(&pendingRenewals)
	if pendingRenewals != 1 {
		t.Errorf("after delivering the requeued auto-invoice: want one pending renewal invoice, got %d", pendingRenewals)
	}
}

// TestIntegration_CancelSubscription_KeepsUnpaidActivation: the unpaid
// activation invoice backs the period the subscription is already in, so a
// cancel leaves it payable; only a renewal invoice is voided.
func TestIntegration_CancelSubscription_KeepsUnpaidActivation(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b27"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
	activation, err := r.insertInvoice(t.Context(), pool, orgID, sub.ID, 900, 0, 0, "USD", invoiceKindActivation, false, nil)
	if err != nil {
		t.Fatalf("seed activation invoice: %v", err)
	}

	if err := inServiceTx(t, pool, func(ctx context.Context) error {
		_, err := mod.svc.cancelSubscription(ctx, subjectTypeOrganization, orgID, "sub_owner", "too_expensive", "")
		return err
	}); err != nil {
		t.Fatalf("cancelSubscription: %v", err)
	}
	if got := invoiceStatus(t, pool, activation.ID); got != "pending" {
		t.Errorf("after cancel: want the activation invoice still pending, got %q", got)
	}
}

// TestIntegration_UndoScheduledCancellation_QueuesOnlyAutoInvoice: undo far
// from the period end queues the auto-invoice for 3 days before that end and
// nothing else — the reminder and expiry check already queued for this end
// stay valid, and a second reminder would notify the owner twice.
func TestIntegration_UndoScheduledCancellation_QueuesOnlyAutoInvoice(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b26"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
	if err := r.scheduleCancellation(t.Context(), pool, sub.ID); err != nil {
		t.Fatalf("scheduleCancellation: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM messaging.outbox WHERE payload->>'org_id' = $1`, orgID); err != nil {
		t.Fatalf("clear outbox: %v", err)
	}

	if err := inServiceTx(t, pool, func(ctx context.Context) error {
		_, err := mod.svc.undoScheduledCancellation(ctx, subjectTypeOrganization, orgID, "sub_owner")
		return err
	}); err != nil {
		t.Fatalf("undoScheduledCancellation: %v", err)
	}

	if n := len(outboxRows(t, pool, events.RoutingKeySubscriptionAutoInvoice, orgID)); n != 1 {
		t.Errorf("want exactly one auto-invoice queued, got %d", n)
	}
	if n := len(outboxRows(t, pool, events.RoutingKeySubscriptionRemind, orgID)); n != 0 {
		t.Errorf("want no second reminder queued, got %d", n)
	}
	if n := len(outboxRows(t, pool, events.RoutingKeySubscriptionCheck, orgID)); n != 0 {
		t.Errorf("want no second expiry check queued, got %d", n)
	}
	var notBefore time.Time
	if err := pool.QueryRow(t.Context(),
		`SELECT not_before FROM messaging.outbox WHERE routing_key = $1 AND payload->>'org_id' = $2`,
		events.RoutingKeySubscriptionAutoInvoice, orgID).Scan(&notBefore); err != nil {
		t.Fatalf("read not_before: %v", err)
	}
	if due := sub.PeriodEnd.AddDate(0, 0, -3); notBefore.Sub(due).Abs() > time.Minute {
		t.Errorf("want the auto-invoice due 3 days before period_end (%s), got %s", due, notBefore)
	}
}
