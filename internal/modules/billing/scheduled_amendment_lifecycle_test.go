package billing

// Full-lifecycle integration tests for scheduled subscription amendments:
// schedule via the real service write paths (downgradeSubscription/
// attachAddon/cancelSubscription), apply via the real renewal worker
// (HandleSubscriptionAutoInvoice) — proving the pieces compose correctly end
// to end, not just that each one's own unit tests pass in isolation.
// Internal package, same reasoning as this package's other amendment test
// files: exercises the service methods directly rather than through gin,
// matching this package's established convention for scheduling-focused
// tests.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/contracts/events"
)

// fakeOrgCommander is a minimal contracts.OrganizationCommander for these
// tests: counts calls and can be told to fail its first N before
// succeeding, without depending on the organization module's real
// member/file removal logic (already covered by this package's own
// downgrade/preview tests).
type fakeOrgCommander struct {
	calls     int
	failCount int
}

func (f *fakeOrgCommander) ResolveDowngradeOverage(
	_ context.Context, _ string, _ []string, _ int, _ bool,
) (contracts.OverageResolution, error) {
	f.calls++
	if f.failCount > 0 {
		f.failCount--
		return contracts.OverageResolution{}, errors.New("fake: resolve downgrade overage failed")
	}
	return contracts.OverageResolution{}, nil
}

// fakeOrgSuspender is a minimal contracts.OrganizationSuspender, recording
// every SuspendOrganization call.
type fakeOrgSuspender struct {
	calls []struct{ orgID, reason string }
}

func (f *fakeOrgSuspender) SuspendOrganization(_ context.Context, orgID, reason string) error {
	f.calls = append(f.calls, struct{ orgID, reason string }{orgID, reason})
	return nil
}

func (f *fakeOrgSuspender) UnsuspendOrganization(_ context.Context, _ string) error { return nil }

// encodeSubscriptionCheckBody builds the event body HandleSubscriptionAutoInvoice
// decodes — only Data matters to events.Decode, so the envelope's other
// fields are left at their zero value.
func encodeSubscriptionCheckBody(subID, subjectID string, expectedEnd time.Time) []byte {
	body, _ := json.Marshal(events.Envelope{
		Data: events.SubscriptionCheck{
			SubscriptionID: subID, SubjectType: subjectTypeOrganization,
			SubjectID: subjectID, ExpectedEnd: expectedEnd,
		},
	})
	return body
}

// TestIntegration_ScheduledAmendmentLifecycle_CombinedApply walks: schedule a
// plan downgrade and an addon quantity decrease on an active subscription via
// the real service write paths, confirm entitlement is unchanged immediately
// after, then advance to renewal via the real worker and confirm both
// amendments apply atomically, the invoiced amount matches an independent
// composeInvoiceAmount call against the post-apply state, and history shows a
// scheduled/applied pair for the downgrade.
func TestIntegration_ScheduledAmendmentLifecycle_CombinedApply(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000c01"
	cleanupAmendmentSubscription(pool, orgID)
	t.Cleanup(func() { cleanupAmendmentSubscription(pool, orgID) })

	mod := NewModuleForTest(pool, nil)
	commander := &fakeOrgCommander{}
	mod.svc.orgCommander = commander

	sub, err := r.insertSubscription(t.Context(), pool, subjectTypeOrganization, orgID, "growth", cycleMonthly, "USD")
	if err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	// Seeded directly at the repo layer, not via attachAddon: a first attach
	// on a non-trialing subscription now gates on payment — this test is
	// about downgrade/cancellation lifecycle, not attach itself, so it needs
	// a live quantity of 3 up front, not a pending invoice.
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 3)

	if _, _, err := mod.svc.downgradeSubscription(t.Context(), subjectTypeOrganization, orgID, "solo", cycleMonthly, "sub_owner", nil); err != nil {
		t.Fatalf("downgradeSubscription: %v", err)
	}
	if _, err := mod.svc.attachAddon(t.Context(), orgID, "extra-seat", 1, "sub_owner"); err != nil {
		t.Fatalf("schedule addon decrease: %v", err)
	}

	// Confirm immediately: nothing live has changed yet.
	fresh, err := r.findSubscriptionByID(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("findSubscriptionByID: %v", err)
	}
	if fresh.Plan != "growth" || fresh.Cycle != cycleMonthly {
		t.Fatalf("want live plan/cycle unchanged immediately after scheduling, got %s/%s", fresh.Plan, fresh.Cycle)
	}
	if fresh.ScheduledPlan == nil || *fresh.ScheduledPlan != "solo" {
		t.Fatalf("want scheduled_plan=solo, got %v", fresh.ScheduledPlan)
	}
	addon, err := r.findAttachedAddon(t.Context(), pool, sub.ID, "extra-seat")
	if err != nil {
		t.Fatalf("findAttachedAddon: %v", err)
	}
	if addon.Quantity != 3 {
		t.Fatalf("want live addon quantity unchanged (3) immediately after scheduling, got %d", addon.Quantity)
	}
	if addon.ScheduledQuantity == nil || *addon.ScheduledQuantity != 1 {
		t.Fatalf("want scheduled_quantity=1, got %v", addon.ScheduledQuantity)
	}

	// Advance to renewal.
	body := encodeSubscriptionCheckBody(sub.ID, orgID, *fresh.PeriodEnd)
	_ = mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body) // payment link fails on empty ProviderConfig, same as every other worker test

	applied, err := r.findSubscriptionByID(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("findSubscriptionByID after renewal: %v", err)
	}
	if applied.Plan != "solo" || applied.Cycle != cycleMonthly {
		t.Errorf("want plan/cycle applied to solo/monthly, got %s/%s", applied.Plan, applied.Cycle)
	}
	if applied.ScheduledPlan != nil || applied.ScheduledCycle != nil || applied.ScheduledRequestedAt != nil {
		t.Errorf("want every scheduled_* column cleared after apply, got %+v", applied)
	}
	appliedAddon, err := r.findAttachedAddon(t.Context(), pool, sub.ID, "extra-seat")
	if err != nil {
		t.Fatalf("findAttachedAddon after renewal: %v", err)
	}
	if appliedAddon.Quantity != 1 || appliedAddon.ScheduledQuantity != nil {
		t.Errorf("want addon quantity applied to 1 with schedule cleared, got quantity=%d scheduled_quantity=%v",
			appliedAddon.Quantity, appliedAddon.ScheduledQuantity)
	}
	if commander.calls != 0 {
		t.Errorf("want no overage resolution needed (usage is 0, well within solo+addon's combined limit), got %d calls", commander.calls)
	}

	var invoiceCount int
	var invoiceAmount int64
	pool.QueryRow(t.Context(), `SELECT COUNT(*), COALESCE(MAX(amount_cents), 0) FROM billing.invoices WHERE subscription_id = $1`, sub.ID).
		Scan(&invoiceCount, &invoiceAmount)
	if invoiceCount != 1 {
		t.Fatalf("want exactly 1 invoice created, got %d", invoiceCount)
	}
	planInfo, err := mod.svc.planCatalog(t.Context(), "solo")
	if err != nil {
		t.Fatalf("planCatalog: %v", err)
	}
	wantSubtotal, _, couponCode, discountCents, err := mod.svc.composeInvoiceAmount(t.Context(), pool, sub.ID, planInfo, "USD", cycleMonthly)
	if err != nil {
		t.Fatalf("composeInvoiceAmount: %v", err)
	}
	if couponCode != "" || discountCents != 0 {
		t.Fatalf("test setup assumption violated: expected no coupon in this scenario, got couponCode=%q discountCents=%d", couponCode, discountCents)
	}
	if invoiceAmount != wantSubtotal {
		t.Errorf("want the invoice's amount_cents to match an independent composeInvoiceAmount call against post-apply state (%d), got %d", wantSubtotal, invoiceAmount)
	}

	history, err := r.listHistory(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("listHistory: %v", err)
	}
	var scheduledCount, appliedCount int
	for _, h := range history {
		if h.Action != actionDowngrade {
			continue
		}
		var phase *string
		pool.QueryRow(t.Context(), `SELECT phase FROM billing.subscription_history WHERE id = $1`, h.ID).Scan(&phase)
		if phase == nil {
			continue
		}
		switch *phase {
		case historyPhaseScheduled:
			scheduledCount++
		case historyPhaseApplied:
			appliedCount++
		}
	}
	if scheduledCount != 1 || appliedCount != 1 {
		t.Errorf("want one scheduled + one applied downgrade history row, got scheduled=%d applied=%d", scheduledCount, appliedCount)
	}
}

// TestIntegration_ScheduledAmendmentLifecycle_CancellationSupersedes covers:
// scheduling a cancellation while a plan downgrade and an addon decrease are
// already scheduled clears both immediately, and advancing to renewal
// suspends the organization, transitions status, and generates no invoice.
func TestIntegration_ScheduledAmendmentLifecycle_CancellationSupersedes(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000c02"
	cleanupAmendmentSubscription(pool, orgID)
	t.Cleanup(func() { cleanupAmendmentSubscription(pool, orgID) })

	mod := NewModuleForTest(pool, nil)
	suspender := &fakeOrgSuspender{}
	mod.svc.orgSuspender = suspender

	sub, err := r.insertSubscription(t.Context(), pool, subjectTypeOrganization, orgID, "growth", cycleMonthly, "USD")
	if err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	// Seeded directly, not via attachAddon — see the CombinedApply test above.
	seedAmendmentAddon(t, pool, r, sub.ID, "extra-seat", 3)
	if _, _, err := mod.svc.downgradeSubscription(t.Context(), subjectTypeOrganization, orgID, "solo", cycleMonthly, "sub_owner", nil); err != nil {
		t.Fatalf("downgradeSubscription: %v", err)
	}
	if _, err := mod.svc.attachAddon(t.Context(), orgID, "extra-seat", 1, "sub_owner"); err != nil {
		t.Fatalf("schedule addon decrease: %v", err)
	}

	if _, err := mod.svc.cancelSubscription(t.Context(), subjectTypeOrganization, orgID, "sub_owner", "too_expensive", ""); err != nil {
		t.Fatalf("cancelSubscription: %v", err)
	}

	superseded, err := r.findSubscriptionByID(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("findSubscriptionByID after cancel schedule: %v", err)
	}
	if superseded.ScheduledPlan != nil || superseded.ScheduledCycle != nil {
		t.Errorf("want the scheduled plan downgrade cleared once cancellation is scheduled, got %+v", superseded)
	}
	if superseded.ScheduledCancelAt == nil {
		t.Fatal("want scheduled_cancel_at set")
	}
	if superseded.Status != statusActive {
		t.Errorf("want status still active until the worker applies the cancellation, got %q", superseded.Status)
	}
	addon, err := r.findAttachedAddon(t.Context(), pool, sub.ID, "extra-seat")
	if err != nil {
		t.Fatalf("findAttachedAddon: %v", err)
	}
	if addon.ScheduledQuantity != nil {
		t.Errorf("want the scheduled addon decrease cleared once cancellation is scheduled, got %v", addon.ScheduledQuantity)
	}

	body := encodeSubscriptionCheckBody(sub.ID, orgID, *superseded.PeriodEnd)
	if err := mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body); err != nil {
		t.Fatalf("HandleSubscriptionAutoInvoice: want nil error applying a scheduled cancellation, got %v", err)
	}

	if len(suspender.calls) != 1 {
		t.Fatalf("want SuspendOrganization called exactly once, got %d", len(suspender.calls))
	}
	if suspender.calls[0].orgID != orgID {
		t.Errorf("want SuspendOrganization called with %q, got %q", orgID, suspender.calls[0].orgID)
	}
	var status string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.subscriptions WHERE id = $1`, sub.ID).Scan(&status)
	if status != statusCancelled {
		t.Errorf("want status=cancelled, got %q", status)
	}
	var invoiceCount int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.invoices WHERE subscription_id = $1`, sub.ID).Scan(&invoiceCount)
	if invoiceCount != 0 {
		t.Errorf("want zero invoices for a cancelled subscription, got %d", invoiceCount)
	}
	var appliedCancelCount int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.subscription_history WHERE subscription_id = $1 AND action = 'cancel' AND phase = 'applied'`,
		sub.ID).Scan(&appliedCancelCount)
	if appliedCancelCount != 1 {
		t.Errorf("want 1 applied cancel history row, got %d", appliedCancelCount)
	}
}

// TestIntegration_ScheduledAmendmentLifecycle_RedeliverySafety schedules a
// downgrade via the real write path whose target plan's limit current usage
// exceeds, fails the first overage-resolution attempt, then redelivers —
// proving the whole schedule-then-apply loop, not just the worker step in
// isolation, leaves nothing half-applied on failure and completes correctly
// on retry.
func TestIntegration_ScheduledAmendmentLifecycle_RedeliverySafety(t *testing.T) {
	pool := testPoolAmendment(t)
	r := &repository{}
	const orgID = "00000000-0000-0000-0000-000000000c03"
	cleanupAmendmentSubscription(pool, orgID)
	t.Cleanup(func() { cleanupAmendmentSubscription(pool, orgID) })

	mod := NewModuleForTest(pool, nil)
	commander := &fakeOrgCommander{failCount: 1}
	mod.svc.orgCommander = commander

	sub, err := r.insertSubscription(t.Context(), pool, subjectTypeOrganization, orgID, "growth", cycleMonthly, "USD")
	if err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	if _, _, err := mod.svc.downgradeSubscription(t.Context(), subjectTypeOrganization, orgID, "solo", cycleMonthly, "sub_owner", nil); err != nil {
		t.Fatalf("downgradeSubscription: %v", err)
	}
	if err := r.upsertUsage(t.Context(), pool, orgID, "members", 2, time.Now(), time.Now().AddDate(0, 1, 0)); err != nil {
		t.Fatalf("seed member usage: %v", err) // solo's limit is 1 — over
	}

	scheduled, err := r.findSubscriptionByID(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("findSubscriptionByID: %v", err)
	}
	body := encodeSubscriptionCheckBody(sub.ID, orgID, *scheduled.PeriodEnd)

	if err := mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body); err == nil {
		t.Fatal("first delivery: want an error from the failed overage resolution")
	}
	afterFailure, err := r.findSubscriptionByID(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("findSubscriptionByID after failure: %v", err)
	}
	if afterFailure.Plan != "growth" || afterFailure.ScheduledPlan == nil || *afterFailure.ScheduledPlan != "solo" {
		t.Fatalf("after a failed overage resolution: want plan=growth with scheduled_plan still solo, got plan=%q scheduled_plan=%v",
			afterFailure.Plan, afterFailure.ScheduledPlan)
	}
	var invoiceCountAfterFailure int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.invoices WHERE subscription_id = $1`, sub.ID).Scan(&invoiceCountAfterFailure)
	if invoiceCountAfterFailure != 0 {
		t.Errorf("want no invoice created after a failed overage resolution, got %d", invoiceCountAfterFailure)
	}

	// Redelivery: the fake now succeeds (failCount exhausted). The overage
	// step's own transaction commits independently of what happens next —
	// createPaymentLink still fails with an empty ProviderConfig, same as
	// every other worker test in this package, so that error is expected.
	_ = mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body)

	afterRetry, err := r.findSubscriptionByID(t.Context(), pool, sub.ID)
	if err != nil {
		t.Fatalf("findSubscriptionByID after retry: %v", err)
	}
	if afterRetry.Plan != "solo" || afterRetry.ScheduledPlan != nil {
		t.Errorf("after successful redelivery: want plan=solo with schedule cleared, got plan=%q scheduled_plan=%v",
			afterRetry.Plan, afterRetry.ScheduledPlan)
	}
	if commander.calls != 2 {
		t.Errorf("want ResolveDowngradeOverage called twice total (failed once, succeeded once), got %d", commander.calls)
	}
	var invoiceCountAfterRetry int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.invoices WHERE subscription_id = $1`, sub.ID).Scan(&invoiceCountAfterRetry)
	if invoiceCountAfterRetry != 1 {
		t.Errorf("want exactly 1 invoice after successful redelivery (not skipped, not doubled), got %d", invoiceCountAfterRetry)
	}
}
