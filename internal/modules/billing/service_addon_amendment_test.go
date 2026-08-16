package billing

// Integration tests for attachAddon/detachAddon's active-subscription
// scheduling branch and undoScheduledAddonQuantityChange. Internal package,
// same reasoning as service_downgrade_amendment_test.go: the "undo" case has
// no HTTP route wired to it yet, and the concurrency test below needs the
// same real per-request transaction the RLS middleware normally provides.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/db"
)

// makeActiveAmendmentSubscription is seedAmendmentSubscription named for
// clarity at each call site — insertSubscription already seeds status
// 'active', asserted explicitly here so a future change to that default
// can't silently turn these active-branch tests into trialing ones.
func makeActiveAmendmentSubscription(t *testing.T, pool *pgxpool.Pool, r *repository, orgID string) *subscriptionRecord {
	t.Helper()
	sub := seedAmendmentSubscription(t, pool, r, orgID)
	if sub.Status != statusActive {
		t.Fatalf("seedAmendmentSubscription: want status=active, got %q", sub.Status)
	}
	return sub
}

// seedAmendmentTrialSubscription inserts a fresh trialing subscription for
// orgID, for tests exercising the trial-immediate branch specifically.
func seedAmendmentTrialSubscription(t *testing.T, pool *pgxpool.Pool, r *repository, orgID string) *subscriptionRecord {
	t.Helper()
	cleanupAmendmentSubscription(pool, orgID)
	t.Cleanup(func() { cleanupAmendmentSubscription(pool, orgID) })
	s, err := r.insertSubscriptionWithTrial(t.Context(), pool, "organization", orgID, "solo", cycleMonthly, "USD", time.Now().Add(7*24*time.Hour))
	if err != nil {
		t.Fatalf("seed trial subscription: %v", err)
	}
	return s
}

func TestIntegration_AttachAddon_Trialing_NewAndDecrease_Immediate(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b13"
	mod := NewModuleForTest(pool, nil)
	sub := seedAmendmentTrialSubscription(t, pool, r, orgID)

	if _, err := mod.svc.attachAddon(t.Context(), orgID, "extra-seat", 5, "sub_owner"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if _, err := mod.svc.attachAddon(t.Context(), orgID, "extra-seat", 2, "sub_owner"); err != nil {
		t.Fatalf("decrease on trial: %v", err)
	}

	addons, err := r.listAttachedAddonsWithPricing(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listAttachedAddonsWithPricing: %v", err)
	}
	if len(addons) != 1 || addons[0].Quantity != 2 {
		t.Fatalf("want live quantity=2 (immediate, trialing), got %+v", addons)
	}
	if addons[0].ScheduledQuantity != nil {
		t.Errorf("want nothing scheduled on trial, got %v", addons[0].ScheduledQuantity)
	}
}

// TestIntegration_AttachAddon_Active_Increase_CreatesPendingInvoice_ClearsStaleSchedule
// covers the core addon-increase behavior on a non-trialing subscription: it
// no longer bumps the live quantity immediately — it creates a gating
// invoice and leaves quantity untouched until that invoice is paid (see the
// webhook-paid lifecycle test further down this package for the other half
// of the round trip). Clearing a stale scheduled decrease still happens
// immediately regardless — that part of the pre-existing behavior is
// unaffected by the payment gate.
func TestIntegration_AttachAddon_Active_Increase_CreatesPendingInvoice_ClearsStaleSchedule(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b14"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 5)
	if err := r.scheduleAddonQuantityChange(t.Context(), pool, sub.ID, "extra-seat", 2); err != nil {
		t.Fatalf("seed stale schedule: %v", err)
	}

	addon, err := mod.svc.attachAddon(t.Context(), orgID, "extra-seat", 8, "sub_owner")
	if err != nil {
		t.Fatalf("increase: %v", err)
	}
	if addon.PendingQuantity == nil || *addon.PendingQuantity != 8 {
		t.Errorf("want pending_quantity=8, got %v", addon.PendingQuantity)
	}
	if addon.PendingInvoiceID == nil {
		t.Error("want pending_invoice_id set")
	}

	addons, err := r.listAttachedAddonsWithPricing(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listAttachedAddonsWithPricing: %v", err)
	}
	if len(addons) != 1 || addons[0].Quantity != 5 {
		t.Fatalf("want live quantity unchanged at 5 until payment, got %+v", addons)
	}
	if addons[0].ScheduledQuantity != nil {
		t.Errorf("want the stale scheduled decrease cleared by the increase, got %v", addons[0].ScheduledQuantity)
	}

	invoices, err := r.listInvoices(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listInvoices: %v", err)
	}
	if len(invoices) != 1 || invoices[0].Kind != "addon_increase" || invoices[0].Status != statusPending {
		t.Fatalf("want one pending addon_increase invoice, got %+v", invoices)
	}
}

func TestIntegration_AttachAddon_Active_Decrease_Schedules(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b15"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 5)

	if _, err := mod.svc.attachAddon(t.Context(), orgID, "extra-seat", 2, "sub_owner"); err != nil {
		t.Fatalf("decrease: %v", err)
	}

	addons, err := r.listAttachedAddonsWithPricing(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listAttachedAddonsWithPricing: %v", err)
	}
	if len(addons) != 1 || addons[0].Quantity != 5 {
		t.Fatalf("want live quantity unchanged at 5, got %+v", addons)
	}
	if addons[0].ScheduledQuantity == nil || *addons[0].ScheduledQuantity != 2 {
		t.Errorf("want scheduled_quantity=2, got %v", addons[0].ScheduledQuantity)
	}
}

func TestIntegration_AttachAddon_Active_CancellationScheduled_DecreaseRejected_IncreaseAllowed(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b16"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 5)
	if err := r.scheduleCancellation(t.Context(), pool, sub.ID); err != nil {
		t.Fatalf("scheduleCancellation: %v", err)
	}

	_, decreaseErr := mod.svc.attachAddon(t.Context(), orgID, "extra-seat", 2, "sub_owner")
	wantApperrCode(t, decreaseErr, cancellationScheduledCode)
	addon, err := mod.svc.attachAddon(t.Context(), orgID, "extra-seat", 9, "sub_owner")
	if err != nil {
		t.Fatalf("increase must still succeed while a cancellation is scheduled: %v", err)
	}
	if addon.PendingQuantity == nil || *addon.PendingQuantity != 9 {
		t.Errorf("want pending_quantity=9, got %v", addon.PendingQuantity)
	}

	addons, err := r.listAttachedAddonsWithPricing(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listAttachedAddonsWithPricing: %v", err)
	}
	if len(addons) != 1 || addons[0].Quantity != 5 {
		t.Fatalf("want live quantity unchanged at 5 (increase still gated on payment), got %+v", addons)
	}
}

func TestIntegration_DetachAddon_Trialing_DeletesImmediately(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b17"
	mod := NewModuleForTest(pool, nil)
	sub := seedAmendmentTrialSubscription(t, pool, r, orgID)
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 3)

	// detachAddon locks the subscription row via s.querier(ctx), which
	// resolves to whatever transaction is already in ctx (the RLS
	// middleware's, in a live request) — wrap the call in a real
	// transaction here, mirroring how the RLS-wrapped route actually calls it.
	if err := db.WithTx(t.Context(), pool, func(tx db.Querier) error {
		return mod.svc.detachAddon(db.WithQuerier(t.Context(), tx), orgID, "extra-seat", "sub_owner")
	}); err != nil {
		t.Fatalf("detach: %v", err)
	}

	addons, err := r.listAttachedAddonsWithPricing(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listAttachedAddonsWithPricing: %v", err)
	}
	if len(addons) != 0 {
		t.Fatalf("want the row deleted on trial, got %+v", addons)
	}
}

func TestIntegration_DetachAddon_Active_SchedulesRemoval_RowSurvives(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b18"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 3)

	if err := db.WithTx(t.Context(), pool, func(tx db.Querier) error {
		return mod.svc.detachAddon(db.WithQuerier(t.Context(), tx), orgID, "extra-seat", "sub_owner")
	}); err != nil {
		t.Fatalf("detach: %v", err)
	}

	addons, err := r.listAttachedAddonsWithPricing(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listAttachedAddonsWithPricing: %v", err)
	}
	if len(addons) != 1 {
		t.Fatalf("want the row to survive until renewal, got %+v", addons)
	}
	if addons[0].Quantity != 3 {
		t.Errorf("want live quantity unchanged at 3, got %d", addons[0].Quantity)
	}
	if addons[0].ScheduledQuantity == nil || *addons[0].ScheduledQuantity != 0 {
		t.Errorf("want scheduled_quantity=0, got %v", addons[0].ScheduledQuantity)
	}
}

func TestIntegration_DetachAddon_Active_CancellationScheduled_Rejected(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b19"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 3)
	if err := r.scheduleCancellation(t.Context(), pool, sub.ID); err != nil {
		t.Fatalf("scheduleCancellation: %v", err)
	}

	err := db.WithTx(t.Context(), pool, func(tx db.Querier) error {
		return mod.svc.detachAddon(db.WithQuerier(t.Context(), tx), orgID, "extra-seat", "sub_owner")
	})
	wantApperrCode(t, err, cancellationScheduledCode)

	addons, err := r.listAttachedAddonsWithPricing(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listAttachedAddonsWithPricing: %v", err)
	}
	if addons[0].ScheduledQuantity != nil {
		t.Errorf("want nothing scheduled when rejected, got %v", addons[0].ScheduledQuantity)
	}
}

func TestIntegration_UndoScheduledAddonQuantityChange_ClearsSchedule(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b1a"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 5)
	if err := r.scheduleAddonQuantityChange(t.Context(), pool, sub.ID, "extra-seat", 2); err != nil {
		t.Fatalf("seed schedule: %v", err)
	}

	var updated *attachedAddonRecord
	err := db.WithTx(t.Context(), pool, func(tx db.Querier) error {
		var txErr error
		updated, txErr = mod.svc.undoScheduledAddonQuantityChange(db.WithQuerier(t.Context(), tx), orgID, "extra-seat", "sub_owner")
		return txErr
	})
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if updated.ScheduledQuantity != nil || updated.ScheduledRequestedAt != nil {
		t.Errorf("want scheduled columns cleared, got %+v", updated)
	}
	if updated.Quantity != 5 {
		t.Errorf("undo must never touch the live quantity, got %d", updated.Quantity)
	}
}

func TestIntegration_UndoScheduledAddonQuantityChange_NothingScheduled(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b1b"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 5)

	err := db.WithTx(t.Context(), pool, func(tx db.Querier) error {
		_, txErr := mod.svc.undoScheduledAddonQuantityChange(db.WithQuerier(t.Context(), tx), orgID, "extra-seat", "sub_owner")
		return txErr
	})
	wantApperrCode(t, err, "NO_SCHEDULED_ADDON_CHANGE")
}

// TestIntegration_AttachAddon_Concurrent_DecreaseAndUndo races attachAddon
// (scheduling a decrease) against undoScheduledAddonQuantityChange on the
// same subscription+addon, each wrapped in its own real transaction the way
// the RLS middleware wraps a live request — reproducing
// lockSubscriptionForUpdate's actual serialization. Whichever write commits
// last decides the outcome; what must never happen is a torn write or
// either call erroring out.
func TestIntegration_AttachAddon_Concurrent_DecreaseAndUndo(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b1c"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 5)
	if err := r.scheduleAddonQuantityChange(t.Context(), pool, sub.ID, "extra-seat", 3); err != nil {
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
			_, err := mod.svc.attachAddon(ctx, orgID, "extra-seat", 2, "sub_owner")
			return err
		})
	}()
	go func() {
		defer wg.Done()
		errs[1] = inTx(func(ctx context.Context) error {
			_, err := mod.svc.undoScheduledAddonQuantityChange(ctx, orgID, "extra-seat", "sub_owner")
			return err
		})
	}()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d: want no error, got %v", i, err)
		}
	}

	addons, err := r.listAttachedAddonsWithPricing(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listAttachedAddonsWithPricing: %v", err)
	}
	if len(addons) != 1 {
		t.Fatalf("want 1 attached addon, got %+v", addons)
	}
	if addons[0].Quantity != 5 {
		t.Errorf("neither call touches the live quantity, want 5, got %d", addons[0].Quantity)
	}
	scheduledTwo := addons[0].ScheduledQuantity != nil && *addons[0].ScheduledQuantity == 2
	cleared := addons[0].ScheduledQuantity == nil
	if !scheduledTwo && !cleared {
		t.Errorf("want either attachAddon's re-schedule (2) or a fully-cleared schedule, never a torn write: %+v", addons[0])
	}
}

// TestIntegration_AttachAddon_Active_SecondIncrease_VoidsAndReissues covers
// the void-and-reissue rule: a second increase request while an earlier one
// is still unpaid voids that earlier invoice (rather than rejecting the new
// request or leaving two live pending invoices on the same addon row) and
// issues a fresh one for the new target quantity.
func TestIntegration_AttachAddon_Active_SecondIncrease_VoidsAndReissues(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b1d"
	mod := NewModuleForTest(pool, nil)
	makeActiveAmendmentSubscription(t, pool, r, orgID)

	first, err := mod.svc.attachAddon(t.Context(), orgID, "extra-seat", 5, "sub_owner")
	if err != nil {
		t.Fatalf("first increase: %v", err)
	}
	if first.PendingInvoiceID == nil {
		t.Fatal("want pending_invoice_id set after first increase")
	}
	firstInvoiceID := *first.PendingInvoiceID

	second, err := mod.svc.attachAddon(t.Context(), orgID, "extra-seat", 8, "sub_owner")
	if err != nil {
		t.Fatalf("second increase: %v", err)
	}
	if second.PendingInvoiceID == nil || *second.PendingInvoiceID == firstInvoiceID {
		t.Fatalf("want a new, different pending_invoice_id after the second request, got %v (first was %s)", second.PendingInvoiceID, firstInvoiceID)
	}
	if second.PendingQuantity == nil || *second.PendingQuantity != 8 {
		t.Errorf("want pending_quantity=8 (the second request's target), got %v", second.PendingQuantity)
	}

	var firstStatus string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, firstInvoiceID).Scan(&firstStatus)
	if firstStatus != "void" {
		t.Errorf("want the first invoice voided by the second request, got status=%q", firstStatus)
	}
	var secondStatus string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, *second.PendingInvoiceID).Scan(&secondStatus)
	if secondStatus != "pending" {
		t.Errorf("want the second (current) invoice still pending, got status=%q", secondStatus)
	}
}

// TestIntegration_AttachAddon_Active_UnrelatedPendingInvoiceUnaffected covers
// the edge case where an addon-increase invoice is independent of any other
// pending invoice on the same subscription (e.g. a renewal) — creating or
// voiding-and-reissuing the addon invoice must never touch it.
func TestIntegration_AttachAddon_Active_UnrelatedPendingInvoiceUnaffected(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b1e"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)

	renewalInv, err := r.insertInvoice(t.Context(), pool, orgID, sub.ID, 900, 0, 0, "USD", "subscription", false, nil)
	if err != nil {
		t.Fatalf("seed renewal invoice: %v", err)
	}
	renewalInvoiceID := renewalInv.ID

	if _, err := mod.svc.attachAddon(t.Context(), orgID, "extra-seat", 5, "sub_owner"); err != nil {
		t.Fatalf("first increase: %v", err)
	}
	if _, err := mod.svc.attachAddon(t.Context(), orgID, "extra-seat", 9, "sub_owner"); err != nil {
		t.Fatalf("second increase (void-and-reissue): %v", err)
	}

	var renewalStatus string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, renewalInvoiceID).Scan(&renewalStatus)
	if renewalStatus != "pending" {
		t.Errorf("want the unrelated renewal invoice untouched (still pending), got status=%q", renewalStatus)
	}

	var invoiceCount int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.invoices WHERE subscription_id = $1`, sub.ID).Scan(&invoiceCount)
	if invoiceCount != 3 { // renewal (pending) + first addon increase (voided) + second (pending)
		t.Errorf("want 3 invoices total (renewal + voided first + current second), got %d", invoiceCount)
	}
}

// TestIntegration_AttachAddon_Active_TwoAddonsIndependentPending covers the
// edge case where two different addons on the same subscription can each
// have their own pending increase invoice simultaneously, with no
// cross-talk — paying one doesn't touch the other's pending state.
func TestIntegration_AttachAddon_Active_TwoAddonsIndependentPending(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b1f"
	seedTestAddonCatalogRow(t, pool)
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)

	seatAddon, err := mod.svc.attachAddon(t.Context(), orgID, "extra-seat", 5, "sub_owner")
	if err != nil {
		t.Fatalf("attach extra-seat: %v", err)
	}
	secondAddon, err := mod.svc.attachAddon(t.Context(), orgID, testAddonID, 10, "sub_owner")
	if err != nil {
		t.Fatalf("attach %s: %v", testAddonID, err)
	}
	if seatAddon.PendingInvoiceID == nil || secondAddon.PendingInvoiceID == nil ||
		*seatAddon.PendingInvoiceID == *secondAddon.PendingInvoiceID {
		t.Fatalf("want two distinct pending invoices, got seat=%v second=%v", seatAddon.PendingInvoiceID, secondAddon.PendingInvoiceID)
	}

	// Pay only the seat addon's invoice.
	if _, err := r.markInvoicePaid(t.Context(), pool, *seatAddon.PendingInvoiceID, time.Now()); err != nil {
		t.Fatalf("markInvoicePaid: %v", err)
	}
	if err := r.applyPendingAddonIncrease(t.Context(), pool, sub.ID, "extra-seat"); err != nil {
		t.Fatalf("applyPendingAddonIncrease: %v", err)
	}

	seatAfter, err := r.findAttachedAddon(t.Context(), pool, sub.ID, "extra-seat")
	if err != nil {
		t.Fatalf("findAttachedAddon extra-seat: %v", err)
	}
	if seatAfter.Quantity != 5 || seatAfter.PendingQuantity != nil {
		t.Errorf("want extra-seat applied (quantity=5, pending cleared), got %+v", seatAfter)
	}

	secondAfter, err := r.findAttachedAddon(t.Context(), pool, sub.ID, testAddonID)
	if err != nil {
		t.Fatalf("findAttachedAddon %s: %v", testAddonID, err)
	}
	if secondAfter.Quantity != 0 || secondAfter.PendingQuantity == nil || *secondAfter.PendingQuantity != 10 {
		t.Errorf("want %s's pending state untouched by the seat addon's payment, got %+v", testAddonID, secondAfter)
	}
}

// TestIntegration_AttachAddon_Active_ProrationReflectsRemainingPeriod covers
// the day-proration boundary cases: a request made on the exact day of
// renewal (~0 days remaining) charges near-zero, and a request made on the
// first day of a fresh period (a full cycle remaining) charges the addon's
// full per-unit price.
func TestIntegration_AttachAddon_Active_ProrationReflectsRemainingPeriod(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}

	t.Run("request on the exact day of renewal charges nothing and applies directly", func(t *testing.T) {
		const orgID = "00000000-0000-0000-0000-000000000b21"
		mod := NewModuleForTest(pool, nil)
		sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
		now := time.Now()
		if err := r.updateSubscriptionPeriod(t.Context(), pool, sub.ID, now.AddDate(0, -1, 0), now); err != nil {
			t.Fatalf("updateSubscriptionPeriod: %v", err)
		}

		// Zero days remaining prorates to a $0 charge — billing.invoices
		// requires amount_cents > 0, so requestAddonIncrease applies the
		// increase directly instead of creating a zero-amount invoice
		// (nothing to gate a $0 charge on payment for anyway).
		addon, err := mod.svc.attachAddon(t.Context(), orgID, "extra-seat", 5, "sub_owner")
		if err != nil {
			t.Fatalf("attach: %v", err)
		}
		if addon.Quantity != 5 || addon.PendingQuantity != nil || addon.PendingInvoiceID != nil {
			t.Errorf("want the increase applied directly (quantity=5, no pending invoice) for a $0 proration, got %+v", addon)
		}
		var invoiceCount int
		pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.invoices WHERE subscription_id = $1`, sub.ID).Scan(&invoiceCount)
		if invoiceCount != 0 {
			t.Errorf("want no invoice created for a $0 proration, got %d", invoiceCount)
		}
	})

	t.Run("request at the start of a fresh full period charges the full per-unit price", func(t *testing.T) {
		const orgID = "00000000-0000-0000-0000-000000000b22"
		mod := NewModuleForTest(pool, nil)
		sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
		now := time.Now()
		if err := r.updateSubscriptionPeriod(t.Context(), pool, sub.ID, now, now.AddDate(0, 0, 30)); err != nil {
			t.Fatalf("updateSubscriptionPeriod: %v", err)
		}

		addon, err := mod.svc.attachAddon(t.Context(), orgID, "extra-seat", 1, "sub_owner")
		if err != nil {
			t.Fatalf("attach: %v", err)
		}
		var amountCents int64
		pool.QueryRow(t.Context(), `SELECT amount_cents FROM billing.invoices WHERE id = $1`, *addon.PendingInvoiceID).Scan(&amountCents)
		// extra-seat's USD monthly price is 100 cents (seed data) x 1 unit,
		// 0% tax by default in this test's unwired taxReader. Allow 1 cent of
		// float-truncation slop (100/30 isn't exact, and int64() truncates
		// toward zero) — the pure-function unit tests cover exact boundary
		// math with evenly-divisible numbers; this integration test only
		// needs to confirm a full period charges (approximately) the full
		// price, not exactly zero or some other wildly different amount.
		if amountCents < 99 || amountCents > 100 {
			t.Errorf("want ~full monthly price (99-100 cents) for a full period remaining, got %d", amountCents)
		}
	})
}

// TestIntegration_AttachAddon_Active_MultiCyclePeriod_ChargesTieredPrice
// guards a subscription whose current period spans more than one billing
// cycle (possible via extendSubscription, up to 24 months): a same-day
// addon increase must price off the period's actual real length, not a flat
// single-cycle price — subscription_addons has no expiry of its own and
// rides period_end, so a seat priced for one cycle but attached for the
// full remaining period would otherwise run free for the difference.
func TestIntegration_AttachAddon_Active_MultiCyclePeriod_ChargesTieredPrice(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b24"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)
	now := time.Now()
	// 24 months = extendSubscription's own lifetime cap (maxRunwayMonths) —
	// 2 full yearly blocks, no remainder.
	if err := r.updateSubscriptionPeriod(t.Context(), pool, sub.ID, now, now.AddDate(0, 24, 0)); err != nil {
		t.Fatalf("updateSubscriptionPeriod: %v", err)
	}

	addon, err := mod.svc.attachAddon(t.Context(), orgID, "extra-seat", 1, "sub_owner")
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	var amountCents int64
	pool.QueryRow(t.Context(), `SELECT amount_cents FROM billing.invoices WHERE id = $1`, *addon.PendingInvoiceID).Scan(&amountCents)
	// extra-seat's USD yearly price is 1000 cents (seed data): 2 blocks x
	// 1000 = 2000 for the full 24-month period. Before the fix this priced
	// at a flat single-cycle price (~100-1000 depending on which branch
	// sub.Cycle happened to hit) regardless of the period actually being
	// twice that long. Same 1-cent float-truncation slop as the sibling
	// full-period test above.
	if amountCents < 1999 || amountCents > 2000 {
		t.Errorf("want ~full 24-month tiered price (1999-2000 cents = 2 yearly blocks), got %d", amountCents)
	}
}

// TestIntegration_AttachAddon_Concurrent_TwoIncreases races two increase
// requests for the same addon against each other, each wrapped in its own
// real transaction — reproducing lockSubscriptionForUpdate's actual
// serialization the way a live request's RLS-equivalent transaction would.
// What must never happen is a torn write, two simultaneously-pending
// invoices on the same addon row, or either call erroring out.
func TestIntegration_AttachAddon_Concurrent_TwoIncreases(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000b23"
	mod := NewModuleForTest(pool, nil)
	sub := makeActiveAmendmentSubscription(t, pool, r, orgID)

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
			_, err := mod.svc.attachAddon(ctx, orgID, "extra-seat", 5, "sub_owner")
			return err
		})
	}()
	go func() {
		defer wg.Done()
		errs[1] = inTx(func(ctx context.Context) error {
			_, err := mod.svc.attachAddon(ctx, orgID, "extra-seat", 7, "sub_owner")
			return err
		})
	}()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d: want no error, got %v", i, err)
		}
	}

	var pendingInvoiceCount int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.invoices WHERE subscription_id = $1 AND status = 'pending'`, sub.ID).
		Scan(&pendingInvoiceCount)
	if pendingInvoiceCount != 1 {
		t.Errorf("want exactly 1 live pending invoice (the other voided by whichever request committed second), got %d", pendingInvoiceCount)
	}

	addon, err := r.findAttachedAddon(t.Context(), pool, sub.ID, "extra-seat")
	if err != nil {
		t.Fatalf("findAttachedAddon: %v", err)
	}
	if addon.Quantity != 0 {
		t.Errorf("neither call touches the live quantity, want 0, got %d", addon.Quantity)
	}
	if addon.PendingQuantity == nil || (*addon.PendingQuantity != 5 && *addon.PendingQuantity != 7) {
		t.Errorf("want pending_quantity to be whichever request committed last (5 or 7), got %v", addon.PendingQuantity)
	}
}
