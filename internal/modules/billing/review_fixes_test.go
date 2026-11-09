package billing_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/platform/httpclient"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// An invoice's line items are what an itemized checkout charges, so they must
// add up to the invoice's subtotal. The plan line once carried the whole
// subtotal, add-ons included, and the add-on lines then counted them again.
func TestInvoiceLines_SumToTheSubtotal_WithAddons(t *testing.T) {
	cases := []struct {
		name, orgID, coupon string
	}{
		{"no coupon", "00000000-0000-0000-0000-00000000f101", ""},
		{"with a percent coupon", "00000000-0000-0000-0000-00000000f102", "F1_LINES_PCT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testPoolBilling(t)
			user := "integ_invoice_lines_" + tc.orgID[len(tc.orgID)-3:]
			setupBillingTest(t, pool, tc.orgID)
			subID := provisionActive(t, pool, tc.orgID, user, "2 days")
			if _, err := pool.Exec(t.Context(), `
				INSERT INTO billing.subscription_addons (subscription_id, addon_id, quantity)
				VALUES ($1, 'extra-seat', 2)`, subID); err != nil {
				t.Fatalf("attach add-on: %v", err)
			}
			if tc.coupon != "" {
				percent := int16(10)
				seedCoupon(t, pool, tc.coupon, couponSeedOpts{DiscountType: "percent", PercentOff: &percent, Cadence: "forever"})
				if _, err := pool.Exec(t.Context(), `
					INSERT INTO billing.coupon_redemptions (coupon_id, subscription_id) VALUES ($1, $2)`, tc.coupon, subID); err != nil {
					t.Fatalf("redeem coupon: %v", err)
				}
			}

			invID := issueRenewal(t, pool, subID, tc.orgID)

			var subtotal, lines, addonLines int64
			if err := pool.QueryRow(t.Context(), `
				SELECT i.subtotal_cents,
				       COALESCE((SELECT SUM(total_cents) FROM billing.invoice_line_items WHERE invoice_id = i.id), 0),
				       COALESCE((SELECT SUM(total_cents) FROM billing.invoice_line_items WHERE invoice_id = i.id AND description NOT LIKE '%plan' AND total_cents > 0), 0)
				FROM billing.invoices i WHERE i.id = $1`, invID).Scan(&subtotal, &lines, &addonLines); err != nil {
				t.Fatalf("read invoice: %v", err)
			}
			if addonLines == 0 {
				t.Fatal("want an add-on line on the invoice")
			}
			if lines != subtotal {
				t.Errorf("line items sum to %d, want the subtotal %d", lines, subtotal)
			}
		})
	}
}

// A paid link is settled. A report arriving after the payment, such as a
// redelivered "completed but unpaid" from an async payment method, must not
// make it look payable again.
func TestWebhook_PaidLinkIgnoresALaterPendingReport(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_paid_link_terminal"
		orgID = "00000000-0000-0000-0000-00000000f108"
		extID = "cs_f108"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "20 days")
	invID := seedInvoice(pool, subID, "USD", 900)
	seedPaymentLink(pool, invID, extID, "stripe", "USD", 900)
	e := billing.NewWebhookModuleEngine(pool, user, orgID)
	postPaidStripeWebhook(t, e, extID)

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe",
		fmt.Sprintf(`{"type":"checkout.session.completed","data":{"object":{"id":%q,"payment_status":"unpaid"}}}`, extID)))
	if w.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", w.Code, w.Body)
	}
	if got := linkStatuses(t, pool, invID); !slices.Equal(got, []string{"paid"}) {
		t.Errorf("link: want paid, got %v", got)
	}
}

// Deleting an organization whose subscription already ended must still close
// its open Resume invoice: paying it would otherwise bring the deleted
// organization's subscription back.
func TestOrganizationDeleted_EndedSubscription_FailsItsResumeInvoice(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_deleted_ended_resume"
		orgID = "00000000-0000-0000-0000-00000000f105"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "10 days")
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET status = 'expired' WHERE id = $1`, subID); err != nil {
		t.Fatalf("expire: %v", err)
	}
	invID := seedInvoice(pool, subID, "USD", 900)
	seedLinkWithStatus(t, pool, invID, "cs_f105_resume", "pending")

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	body := encodeEnvelope(t, events.RoutingKeyOrganizationDeleted,
		events.OrganizationDeleted{OrganizationID: orgID, DeletedAt: time.Now()})
	if err := mod.Worker.HandleOrganizationDeleted(t.Context(), body); err != nil {
		t.Fatalf("organization deleted: %v", err)
	}

	if got := invoiceStatus(t, pool, invID); got != "failed" {
		t.Errorf("resume invoice: want failed, got %q", got)
	}
	want := []events.PaymentLinkExpire{{Provider: "stripe", ExternalID: "cs_f105_resume"}}
	if got := checkoutExpiries(t, pool, orgID); !slices.Equal(got, want) {
		t.Errorf("checkout expiries: want %v, got %v", want, got)
	}
	if got := subscriptionStatus(t, pool, subID); got != "expired" {
		t.Errorf("subscription: want it left expired, got %q", got)
	}
}

// voidingTransport answers the provider's create-checkout call, but voids the
// invoice first, as a concurrent cancel or reprice would while the call is in flight.
type voidingTransport struct {
	pool      *pgxpool.Pool
	invoiceID string
}

func (vt voidingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if _, err := vt.pool.Exec(context.Background(),
		`UPDATE billing.invoices SET status = 'void' WHERE id = $1`, vt.invoiceID); err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK, Header: make(http.Header), Request: req,
		Body: io.NopCloser(strings.NewReader(`{"id":"cs_f106_late","url":"https://checkout.example/f106"}`)),
	}, nil
}

// A checkout created for an invoice that closed during the provider call must
// not be left open: paid, it would take money that settles nothing.
func TestCreatePaymentLink_InvoiceVoidedDuringTheProviderCall_ClosesTheCheckout(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_link_void_race"
		orgID = "00000000-0000-0000-0000-00000000f106"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "20 days")
	invID := seedInvoice(pool, subID, "USD", 900)

	prev := httpclient.TrustedClient.Transport
	httpclient.TrustedClient.Transport = voidingTransport{pool: pool, invoiceID: invID}
	t.Cleanup(func() { httpclient.TrustedClient.Transport = prev })

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	err := mod.CreatePaymentLinkForTest(t.Context(), invID)
	if !errors.Is(err, billing.ErrInvoiceNotPayable) {
		t.Errorf("create link: want ErrInvoiceNotPayable, got %v", err)
	}
	if got := linkStatuses(t, pool, invID); len(got) != 0 {
		t.Errorf("links on the void invoice: want none, got %v", got)
	}
	var queued int
	if err := pool.QueryRow(t.Context(), `
		SELECT COUNT(*) FROM messaging.outbox
		WHERE routing_key = $1 AND payload->'data'->>'external_id' = 'cs_f106_late'`,
		events.RoutingKeyPaymentLinkExpire).Scan(&queued); err != nil {
		t.Fatalf("count expiries: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM messaging.outbox WHERE payload->'data'->>'external_id' = 'cs_f106_late'`)
	})
	if queued != 1 {
		t.Errorf("checkout expiry for the late checkout: want 1, got %d", queued)
	}
}

// A Studio trial extension moves the trial's end and the period's with it, and
// queues renewal for the new end. Before, only trial_end moved: the conversion
// invoice was still issued for the old date and the granted days were lost.
func TestOperatorTrialExtend_MovesTheEndAndReschedulesRenewal(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_operator_trial_extend"
		orgID = "00000000-0000-0000-0000-00000000f104"
	)
	setupBillingTest(t, pool, orgID)
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	var trialEnd time.Time
	if err := pool.QueryRow(t.Context(), `SELECT trial_end FROM billing.subscriptions WHERE id = $1 AND status = 'trialing'`, subID).Scan(&trialEnd); err != nil {
		t.Fatalf("read trial: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM messaging.outbox WHERE payload->>'org_id' = $1`, orgID); err != nil {
		t.Fatalf("clear outbox: %v", err)
	}
	newEnd := trialEnd.AddDate(0, 0, 7)
	body := encodeEnvelope(t, events.RoutingKeyOperatorTrialExtend, events.OperatorTrialExtend{OrgID: orgID, TrialEnd: newEnd})
	for range 2 { // a redelivery must not extend twice
		if err := mod.Worker.HandleOperatorTrialExtend(t.Context(), body); err != nil {
			t.Fatalf("trial extend: %v", err)
		}
	}

	var gotTrial, gotPeriod time.Time
	if err := pool.QueryRow(t.Context(), `SELECT trial_end, period_end FROM billing.subscriptions WHERE id = $1`, subID).Scan(&gotTrial, &gotPeriod); err != nil {
		t.Fatalf("read subscription: %v", err)
	}
	if !gotTrial.Equal(newEnd.Truncate(time.Microsecond)) || !gotPeriod.Equal(gotTrial) {
		t.Errorf("want trial_end and period_end at %s, got %s and %s", newEnd, gotTrial, gotPeriod)
	}
	queued := queuedPayloads(t, pool, events.RoutingKeySubscriptionAutoInvoice, orgID)
	if len(queued) != 1 {
		t.Fatalf("auto-invoice for the new end: want 1 queued, got %d", len(queued))
	}
	check, err := events.Decode[events.SubscriptionCheck](queued[0])
	if err != nil {
		t.Fatalf("decode check: %v", err)
	}
	if !check.ExpectedEnd.Equal(gotTrial) {
		t.Errorf("auto-invoice check: want the new trial end, got %s", check.ExpectedEnd)
	}
}

// A Studio plan change goes through billing's own plan change, so a renewal
// priced at the old plan does not survive it.
func TestOperatorPlanChange_RepricesThePendingRenewal(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_operator_plan_change"
		orgID = "00000000-0000-0000-0000-00000000f111"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "2 days")
	renewal := issueRenewal(t, pool, subID, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	body := encodeEnvelope(t, events.RoutingKeyOperatorPlanChange, events.OperatorPlanChange{OrgID: orgID, Plan: "growth", Cycle: "monthly"})
	if err := mod.Worker.HandleOperatorPlanChange(t.Context(), body); err != nil {
		t.Fatalf("plan change: %v", err)
	}

	var plan string
	pool.QueryRow(t.Context(), `SELECT plan FROM billing.subscriptions WHERE id = $1`, subID).Scan(&plan)
	if plan != "growth" {
		t.Errorf("plan: want growth, got %q", plan)
	}
	if got := invoiceStatus(t, pool, renewal); got != "void" {
		t.Errorf("renewal priced at the old plan: want void, got %q", got)
	}

	unknown := encodeEnvelope(t, events.RoutingKeyOperatorPlanChange, events.OperatorPlanChange{OrgID: orgID, Plan: "no-such-plan", Cycle: "monthly"})
	if err := mod.Worker.HandleOperatorPlanChange(t.Context(), unknown); err != nil {
		t.Errorf("an unknown plan is dropped, not retried: got %v", err)
	}
}

// A plan with no price, which only an operator can set, buys no time to prorate
// into: the period must stay. Prorating to it ended the period at once, and the
// expiry check suspended the organization seconds later.
func TestOperatorPlanChange_ToAPlanWithNoPrice_KeepsThePeriod(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_operator_plan_custom"
		orgID = "00000000-0000-0000-0000-00000000f112"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "10 days")
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET plan = 'growth' WHERE id = $1`, subID); err != nil {
		t.Fatalf("set growth: %v", err)
	}
	before, _ := getSubscriptionExpectedEnd(pool, subID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	body := encodeEnvelope(t, events.RoutingKeyOperatorPlanChange, events.OperatorPlanChange{OrgID: orgID, Plan: "custom", Cycle: "monthly"})
	if err := mod.Worker.HandleOperatorPlanChange(t.Context(), body); err != nil {
		t.Fatalf("plan change: %v", err)
	}
	after, _ := getSubscriptionExpectedEnd(pool, subID)
	var plan string
	pool.QueryRow(t.Context(), `SELECT plan FROM billing.subscriptions WHERE id = $1`, subID).Scan(&plan)
	if plan != "custom" || !after.Equal(before) {
		t.Errorf("want plan custom with period_end kept at %s, got %q ending %s", before, plan, after)
	}
}

// A renewal priced from a scheduled downgrade and paid after the subscription
// expired, once the schedule applied at that boundary, bought exactly what the
// subscription now is: it must reactivate, not be parked as unapplied.
func TestLatePayment_AfterTheScheduleAppliedAtExpiry_Recovers(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_late_after_boundary"
		orgID = "00000000-0000-0000-0000-00000000f113"
		extID = "stripe_sess_f113"
	)
	setupBillingTest(t, pool, orgID)
	subID, invID := seedClosedInvoiceState(t, pool, orgID, user, extID, "expired", "subscription", "failed")
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO billing.subscription_history (subscription_id, action, from_plan, to_plan, currency, changed_by, phase)
		VALUES ($1, 'downgrade', 'growth', 'solo', 'USD', 'system', 'applied')`, subID); err != nil {
		t.Fatalf("seed boundary apply: %v", err)
	}
	postPaidStripeWebhook(t, billing.NewWebhookModuleEngineWithOrgReader(pool, user, orgID, &stubOrgSuspender{}, nil), extID)

	if got := subscriptionStatus(t, pool, subID); got != "active" {
		t.Errorf("subscription: want reactivated, got %q", got)
	}
	if got := invoiceStatus(t, pool, invID); got != "paid" {
		t.Errorf("invoice: want paid, got %q", got)
	}
}

// Studio's Activate clears trial_end. The renewal messages queued for the
// trial must still apply to the period that ends at the same moment, or no
// renewal invoice is ever issued and the subscription expires unbilled.
func TestRenewal_AfterStudioActivatesATrial_IsStillIssued(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_renewal_after_activate"
		orgID = "00000000-0000-0000-0000-00000000f114"
	)
	setupBillingTest(t, pool, orgID)
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	queued := queuedPayloads(t, pool, events.RoutingKeySubscriptionAutoInvoice, orgID)
	if len(queued) == 0 {
		t.Fatal("want an auto-invoice queued for the trial")
	}
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET status = 'active', trial_end = NULL WHERE id = $1`, subID); err != nil {
		t.Fatalf("activate like Studio: %v", err)
	}
	useProviderTransport(t, http.StatusInternalServerError)
	_ = mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), queued[len(queued)-1])

	if n := len(pendingInvoiceIDs(t, pool, subID, "subscription")); n != 1 {
		t.Errorf("renewal after Studio activation: want 1 pending, got %d", n)
	}
}

// Paying an activation grants the cycle it was priced for, which can move
// period_end a long way. A schedule waiting for the old end must follow it, or
// it reads as paid for and applies early, after one month of a paid year.
func TestActivationPayment_CycleGrant_MovesAWaitingSchedule(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_activation_moves_schedule"
		orgID = "00000000-0000-0000-0000-00000000f115"
	)
	subID, invID := seedUnpaidActivation(t, pool, orgID, user)
	if _, err := pool.Exec(t.Context(), `
		UPDATE billing.subscriptions
		SET plan = 'growth', cycle = 'yearly', scheduled_plan = 'solo', scheduled_cycle = 'yearly',
		    scheduled_requested_at = now(), scheduled_effective_at = period_end
		WHERE id = $1`, subID); err != nil {
		t.Fatalf("seed schedule: %v", err)
	}
	seedPaymentLink(pool, invID, "cs_f115", "stripe", "USD", 900)
	postPaidStripeWebhook(t, billing.NewWebhookModuleEngine(pool, user, orgID), "cs_f115")

	var periodEnd, effective time.Time
	if err := pool.QueryRow(t.Context(),
		`SELECT period_end, scheduled_effective_at FROM billing.subscriptions WHERE id = $1`, subID).Scan(&periodEnd, &effective); err != nil {
		t.Fatalf("read subscription: %v", err)
	}
	if !effective.Equal(periodEnd) {
		t.Errorf("want the schedule moved to the granted end %s, got %s", periodEnd, effective)
	}
}
