package billing_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/httpclient"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// issueRenewal runs the real auto-invoice for subID and returns the renewal it
// left pending. The payment-link step fails with no provider configured, which is
// fine: the invoice is committed before it.
func issueRenewal(t *testing.T, pool *pgxpool.Pool, subID, orgID string) string {
	t.Helper()
	runAutoInvoice(t, pool, subID, orgID)
	ids := pendingInvoiceIDs(t, pool, subID, "subscription")
	if len(ids) != 1 {
		t.Fatalf("want one pending renewal, got %d", len(ids))
	}
	return ids[0]
}

func runAutoInvoice(t *testing.T, pool *pgxpool.Pool, subID, orgID string) {
	t.Helper()
	useProviderTransport(t, http.StatusInternalServerError)
	end, isTrial := getSubscriptionExpectedEnd(pool, subID)
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	_ = mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), encodeSubscriptionCheckEvent(subID, orgID, end, isTrial))
}

// requestAddon asks for quantity seats and returns the response.
func requestAddon(t *testing.T, e *gin.Engine, orgID string, quantity int) *httptest.ResponseRecorder {
	t.Helper()
	useProviderTransport(t, http.StatusInternalServerError)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/addons",
		fmt.Sprintf(`{"addon_id":"extra-seat","quantity":%d}`, quantity)))
	return w
}

// requestPendingAddon asks for quantity seats on a paid subscription and returns
// the invoice gating them.
func requestPendingAddon(t *testing.T, e *gin.Engine, pool *pgxpool.Pool, subID, orgID string, quantity int) string {
	t.Helper()
	if w := requestAddon(t, e, orgID, quantity); w.Code != http.StatusOK {
		t.Fatalf("attach: want 200, got %d: %s", w.Code, w.Body)
	}
	ids := pendingInvoiceIDs(t, pool, subID, "addon_increase")
	if len(ids) != 1 {
		t.Fatalf("want one pending add-on invoice, got %d", len(ids))
	}
	return ids[0]
}

// requestExtension asks for one more month and returns the invoice for it.
func requestExtension(t *testing.T, e *gin.Engine, pool *pgxpool.Pool, subID, orgID string) string {
	t.Helper()
	useProviderTransport(t, http.StatusInternalServerError)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/extend", `{"months":1}`))
	if w.Code != http.StatusOK {
		t.Fatalf("extend: want 200, got %d: %s", w.Code, w.Body)
	}
	ids := pendingInvoiceIDs(t, pool, subID, "extension")
	if len(ids) != 1 {
		t.Fatalf("want one pending extension, got %d", len(ids))
	}
	return ids[0]
}

// payByWebhook pays invoiceID through a checkout of its own.
func payByWebhook(t *testing.T, e *gin.Engine, pool *pgxpool.Pool, invoiceID string) {
	t.Helper()
	externalID := "cs_paid_" + invoiceID
	seedPaymentLink(pool, invoiceID, externalID, "stripe", "USD", invoiceAmount(t, pool, invoiceID))
	postPaidStripeWebhook(t, e, externalID)
}

func invoiceStatus(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("read invoice status: %v", err)
	}
	return status
}

func invoiceAmount(t *testing.T, pool *pgxpool.Pool, id string) int64 {
	t.Helper()
	var amount int64
	if err := pool.QueryRow(t.Context(), `SELECT amount_cents FROM billing.invoices WHERE id = $1`, id).Scan(&amount); err != nil {
		t.Fatalf("read invoice amount: %v", err)
	}
	return amount
}

func subscriptionStatus(t *testing.T, pool *pgxpool.Pool, subID string) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(t.Context(), `SELECT status FROM billing.subscriptions WHERE id = $1`, subID).Scan(&status); err != nil {
		t.Fatalf("read subscription status: %v", err)
	}
	return status
}

func liveSeats(t *testing.T, pool *pgxpool.Pool, subID string) int {
	t.Helper()
	var quantity int
	if err := pool.QueryRow(t.Context(), `
		SELECT COALESCE(SUM(quantity), 0) FROM billing.subscription_addons WHERE subscription_id = $1`, subID).Scan(&quantity); err != nil {
		t.Fatalf("read seats: %v", err)
	}
	return quantity
}

func couponApplied(t *testing.T, pool *pgxpool.Pool, code, subID string) int {
	t.Helper()
	var applied int
	if err := pool.QueryRow(t.Context(), `
		SELECT applied_count FROM billing.coupon_redemptions WHERE coupon_id = $1 AND subscription_id = $2`,
		code, subID).Scan(&applied); err != nil {
		t.Fatalf("read coupon applications: %v", err)
	}
	return applied
}

func invoiceCount(t *testing.T, pool *pgxpool.Pool, subID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.invoices WHERE subscription_id = $1`, subID).Scan(&n); err != nil {
		t.Fatalf("count invoices: %v", err)
	}
	return n
}

func redeem(t *testing.T, pool *pgxpool.Pool, user, orgID, code string) {
	t.Helper()
	w := httptest.NewRecorder()
	billing.NewModuleEngineWithRealRLS(pool, user, orgID, stubRefReader{}).ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/coupons/redeem", `{"code":"`+code+`"}`))
	if w.Code != http.StatusNoContent {
		t.Fatalf("redeem %s: want 204, got %d: %s", code, w.Code, w.Body)
	}
}

// An add-on raised and paid while a renewal is pending would otherwise ride that
// cheaper renewal for a whole cycle, every cycle, by scheduling its removal again.
// Past-due is the case the auto-invoice ignores, so a void with no reissue would
// leave nothing to pay at all.
func TestIntegration_AddonPaid_RepricesPendingRenewal(t *testing.T) {
	cases := []struct{ status, orgID string }{
		{"active", "00000000-0000-0000-0000-00000000c501"},
		{"past_due", "00000000-0000-0000-0000-00000000c502"},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			pool := testPoolBilling(t)
			user := "integ_stale_addon_renewal_" + tc.status
			setupBillingTest(t, pool, tc.orgID)
			subID := provisionActive(t, pool, tc.orgID, user, "2 days")
			renewal := issueRenewal(t, pool, subID, tc.orgID)
			if got := invoiceAmount(t, pool, renewal); got != 900 {
				t.Fatalf("renewal before the add-on: want 900, got %d", got)
			}
			seedLinkWithStatus(t, pool, renewal, "cs_c50_renewal_"+tc.status, "pending")
			if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET status = $2 WHERE id = $1`, subID, tc.status); err != nil {
				t.Fatalf("set status: %v", err)
			}
			_, endBefore := periodOf(t, pool, subID)

			e := billing.NewWebhookModuleEngine(pool, user, tc.orgID)
			payByWebhook(t, e, pool, requestPendingAddon(t, e, pool, subID, tc.orgID, 5))

			if got := liveSeats(t, pool, subID); got != 5 {
				t.Errorf("seats after paying: want 5, got %d", got)
			}
			if got := invoiceStatus(t, pool, renewal); got != "void" {
				t.Errorf("old renewal: want void, got %q", got)
			}
			if got := checkoutExpiries(t, pool, tc.orgID); !slices.ContainsFunc(got, func(l events.PaymentLinkExpire) bool {
				return l.ExternalID == "cs_c50_renewal_"+tc.status
			}) {
				t.Errorf("old renewal's checkout was not closed at the provider: %v", got)
			}
			pending := pendingInvoiceIDs(t, pool, subID, "subscription")
			if len(pending) != 1 {
				t.Fatalf("want the renewal reissued, got %d pending", len(pending))
			}
			if got := invoiceAmount(t, pool, pending[0]); got != 1400 {
				t.Errorf("reissued renewal: want 1400 (900 plan + 5 x 100 seats), got %d", got)
			}
			if got := subscriptionStatus(t, pool, subID); got != tc.status {
				t.Errorf("status: want %q unchanged, got %q", tc.status, got)
			}
			if _, end := periodOf(t, pool, subID); !end.Equal(endBefore) {
				t.Errorf("period_end moved from %s to %s", endBefore, end)
			}
			created := 0
			for _, payload := range queuedPayloads(t, pool, events.RoutingKeyInvoiceCreated, tc.orgID) {
				evt, err := events.Decode[events.InvoiceCreated](payload)
				if err != nil {
					t.Fatalf("decode invoice created: %v", err)
				}
				if evt.InvoiceID == pending[0] {
					created++
				}
			}
			if created != 1 {
				t.Errorf("want one InvoiceCreated for the reissued renewal, got %d", created)
			}
		})
	}
}

// An extension's add-on lines are priced from the quantity it was requested at, so
// raising the add-on and paying both would buy the new quantity for those months at
// the old price.
func TestIntegration_AddonPaid_DropsPendingExtension(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_addon_extension"
		orgID = "00000000-0000-0000-0000-00000000c503"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "20 days")
	e := billing.NewWebhookModuleEngine(pool, user, orgID)
	extension := requestExtension(t, e, pool, subID, orgID)

	payByWebhook(t, e, pool, requestPendingAddon(t, e, pool, subID, orgID, 5))

	if got := invoiceStatus(t, pool, extension); got != "void" {
		t.Errorf("extension: want void, got %q", got)
	}
}

// Paying a renewal or an extension moves the period, and an add-on invoice was
// priced for the days left in the old one. Paid afterwards, it would carry the new
// quantity through the whole new period.
func TestIntegration_PeriodPayment_VoidsAddonPricedForTheOldPeriod(t *testing.T) {
	cases := []struct {
		name, orgID, periodEnd string
		issue                  func(t *testing.T, e *gin.Engine, pool *pgxpool.Pool, subID, orgID string) string
	}{
		{"renewal", "00000000-0000-0000-0000-00000000c504", "2 days",
			func(t *testing.T, _ *gin.Engine, pool *pgxpool.Pool, subID, orgID string) string {
				t.Helper()
				return issueRenewal(t, pool, subID, orgID)
			}},
		{"extension", "00000000-0000-0000-0000-00000000c505", "20 days", requestExtension},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testPoolBilling(t)
			user := "integ_stale_period_addon_" + tc.name
			setupBillingTest(t, pool, tc.orgID)
			subID := provisionActive(t, pool, tc.orgID, user, tc.periodEnd)
			e := billing.NewWebhookModuleEngine(pool, user, tc.orgID)
			periodInvoice := tc.issue(t, e, pool, subID, tc.orgID)
			addonInvoice := requestPendingAddon(t, e, pool, subID, tc.orgID, 5)
			seedLinkWithStatus(t, pool, addonInvoice, "cs_c50_addon_"+tc.name, "pending")
			_, endBefore := periodOf(t, pool, subID)

			payByWebhook(t, e, pool, periodInvoice)

			if _, end := periodOf(t, pool, subID); !end.After(endBefore) {
				t.Fatalf("the period did not move: %s", end)
			}
			if got := invoiceStatus(t, pool, addonInvoice); got != "void" {
				t.Errorf("add-on invoice: want void, got %q", got)
			}
			var pendingQuantity *int
			if err := pool.QueryRow(t.Context(), `
				SELECT pending_quantity FROM billing.subscription_addons WHERE subscription_id = $1 AND addon_id = 'extra-seat'`,
				subID).Scan(&pendingQuantity); err != nil {
				t.Fatalf("read pending quantity: %v", err)
			}
			if pendingQuantity != nil {
				t.Errorf("want the pending increase cleared, got %d", *pendingQuantity)
			}

			// A payment still landing on that voided checkout buys nothing.
			postPaidStripeWebhook(t, e, "cs_c50_addon_"+tc.name)
			if got := liveSeats(t, pool, subID); got != 0 {
				t.Errorf("seats after a late add-on payment: want 0, got %d", got)
			}
		})
	}
}

// A paid extension covers the time a pending renewal was for. Left open, its
// checkout's expiry would past-due a subscription with months left, and paying it
// would stack another cycle on top.
func TestIntegration_ExtensionPaid_VoidsPendingRenewal(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_extension_renewal"
		orgID = "00000000-0000-0000-0000-00000000c506"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "2 days")
	e := billing.NewWebhookModuleEngine(pool, user, orgID)
	extension := requestExtension(t, e, pool, subID, orgID)
	renewal := issueRenewal(t, pool, subID, orgID)

	payByWebhook(t, e, pool, extension)

	if got := invoiceStatus(t, pool, renewal); got != "void" {
		t.Errorf("renewal: want void, got %q", got)
	}
	if got := subscriptionStatus(t, pool, subID); got != "active" {
		t.Errorf("status: want active, got %q", got)
	}
}

// Both pay for time past the current end, so the second would stack on the first
// and could pass the runway cap Extend enforces when it is requested.
func TestIntegration_RenewalPaid_VoidsPendingExtension(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_renewal_extension"
		orgID = "00000000-0000-0000-0000-00000000c507"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "2 days")
	e := billing.NewWebhookModuleEngine(pool, user, orgID)
	extension := requestExtension(t, e, pool, subID, orgID)
	renewal := issueRenewal(t, pool, subID, orgID)

	payByWebhook(t, e, pool, renewal)

	if got := invoiceStatus(t, pool, extension); got != "void" {
		t.Errorf("extension: want void, got %q", got)
	}
}

// An unpaid purchase additional to the period must not stop the renewal: nothing
// else offers that invoice, and the subscription would expire with no way to pay.
func TestIntegration_AutoInvoice_IssuedDespiteAdditiveInvoices(t *testing.T) {
	cases := []struct {
		name, orgID string
		seed        func(t *testing.T, e *gin.Engine, pool *pgxpool.Pool, subID, orgID string) string
	}{
		{"add-on increase", "00000000-0000-0000-0000-00000000c508",
			func(t *testing.T, e *gin.Engine, pool *pgxpool.Pool, subID, orgID string) string {
				t.Helper()
				return requestPendingAddon(t, e, pool, subID, orgID, 5)
			}},
		{"extension", "00000000-0000-0000-0000-00000000c509", requestExtension},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testPoolBilling(t)
			user := "integ_stale_autoinvoice_" + tc.orgID[len(tc.orgID)-3:]
			setupBillingTest(t, pool, tc.orgID)
			subID := provisionActive(t, pool, tc.orgID, user, "2 days")
			additive := tc.seed(t, billing.NewWebhookModuleEngine(pool, user, tc.orgID), pool, subID, tc.orgID)

			runAutoInvoice(t, pool, subID, tc.orgID)

			pending := pendingInvoiceIDs(t, pool, subID, "subscription")
			if len(pending) != 1 {
				t.Fatalf("want one renewal issued, got %d", len(pending))
			}
			if got := invoiceStatus(t, pool, additive); got != "pending" {
				t.Errorf("additive invoice: want it left pending, got %q", got)
			}

			runAutoInvoice(t, pool, subID, tc.orgID)
			if again := pendingInvoiceIDs(t, pool, subID, "subscription"); len(again) != 1 || again[0] != pending[0] {
				t.Errorf("a redelivery must reuse the renewal, got %v", again)
			}
		})
	}
}

// A trial applies add-on changes at once, with no payment in between, so the
// invoice converting the trial must follow them in both directions.
func TestIntegration_TrialAddonChange_RepricesConversionInvoice(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_trial_addon"
		orgID = "00000000-0000-0000-0000-00000000c50a"
	)
	setupBillingTest(t, pool, orgID)
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	first := issueRenewal(t, pool, subID, orgID)
	e := billing.NewWebhookModuleEngine(pool, user, orgID)

	if w := requestAddon(t, e, orgID, 5); w.Code != http.StatusOK {
		t.Fatalf("attach: want 200, got %d: %s", w.Code, w.Body)
	}
	if got := invoiceStatus(t, pool, first); got != "void" {
		t.Errorf("first invoice: want void, got %q", got)
	}
	raised := pendingInvoiceIDs(t, pool, subID, "subscription")
	if len(raised) != 1 || invoiceAmount(t, pool, raised[0]) != 1400 {
		t.Fatalf("after raising: want one invoice of 1400, got %v", raised)
	}

	if w := requestAddon(t, e, orgID, 2); w.Code != http.StatusOK {
		t.Fatalf("decrease: want 200, got %d: %s", w.Code, w.Body)
	}
	lowered := pendingInvoiceIDs(t, pool, subID, "subscription")
	if len(lowered) != 1 || invoiceAmount(t, pool, lowered[0]) != 1100 {
		t.Fatalf("after lowering: want one invoice of 1100, got %v", lowered)
	}

	w := httptest.NewRecorder()
	billing.NewModuleEngineWithRealRLS(pool, user, orgID, stubRefReader{}).ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodDelete, billingURL(orgID)+"/addons/extra-seat", ""))
	if w.Code >= http.StatusMultipleChoices {
		t.Fatalf("detach: got %d: %s", w.Code, w.Body)
	}
	removed := pendingInvoiceIDs(t, pool, subID, "subscription")
	if len(removed) != 1 || invoiceAmount(t, pool, removed[0]) != 900 {
		t.Fatalf("after removing: want one invoice of 900, got %v", removed)
	}
}

// A charge that prorates to nothing applies at once, so nothing gates it and no
// webhook reprices the renewal afterwards.
func TestIntegration_ZeroChargeAddon_RepricesPendingRenewal(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_zero_addon"
		orgID = "00000000-0000-0000-0000-00000000c50b"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "10 minutes")
	renewal := issueRenewal(t, pool, subID, orgID)

	e := billing.NewWebhookModuleEngine(pool, user, orgID)
	if w := requestAddon(t, e, orgID, 1); w.Code != http.StatusOK {
		t.Fatalf("attach: want 200, got %d: %s", w.Code, w.Body)
	}
	if got := liveSeats(t, pool, subID); got != 1 {
		t.Fatalf("seats: want the increase applied at once, got %d", got)
	}
	if got := invoiceStatus(t, pool, renewal); got != "void" {
		t.Errorf("old renewal: want void, got %q", got)
	}
	pending := pendingInvoiceIDs(t, pool, subID, "subscription")
	if len(pending) != 1 || invoiceAmount(t, pool, pending[0]) != 1000 {
		t.Fatalf("want one renewal of 1000 (900 + 100), got %v", pending)
	}
}

// An increase while the first invoice is unpaid has no period to prorate against,
// and a plan change reissuing that invoice would charge the add-on a second time.
func TestIntegration_AttachAddon_UnpaidActivation_Refused(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_activation_addon"
		orgID = "00000000-0000-0000-0000-00000000c50c"
	)
	subID, activation := seedUnpaidActivation(t, pool, orgID, user)

	w := requestAddon(t, billing.NewWebhookModuleEngine(pool, user, orgID), orgID, 10)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("attach: want 422, got %d: %s", w.Code, w.Body)
	}
	if got := responseErrorCode(t, w); got != "PERIOD_INVOICE_PENDING" {
		t.Errorf("code: want PERIOD_INVOICE_PENDING, got %q", got)
	}
	if got := invoiceCount(t, pool, subID); got != 1 {
		t.Errorf("want only the activation invoice, got %d", got)
	}
	if got := invoiceStatus(t, pool, activation); got != "pending" {
		t.Errorf("activation: want it left pending, got %q", got)
	}
}

// A plan change can leave a period under half a month, which rounded to zero months and
// made an increase free. A monthly rate still applies.
func TestIntegration_AttachAddon_ShortPeriod_IsNotFree(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_short_period"
		orgID = "00000000-0000-0000-0000-00000000c515"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "5 days")
	e := billing.NewWebhookModuleEngine(pool, user, orgID)

	inv := requestPendingAddon(t, e, pool, subID, orgID, 10)

	// Ten seats at 100 a month, for five of the six days the period spans.
	if got := invoiceAmount(t, pool, inv); got < 800 || got > 840 {
		t.Errorf("amount: want about 833, got %d", got)
	}
	if got := liveSeats(t, pool, subID); got != 0 {
		t.Errorf("seats: want none until paid, got %d", got)
	}
}

// A voided invoice no longer used the discount it was issued with, so whatever
// replaces it must not lose a discount the customer never paid with.
func TestIntegration_Void_ReleasesTheCouponApplication(t *testing.T) {
	cases := []struct {
		name, orgID string
		act         func(t *testing.T, pool *pgxpool.Pool, user, orgID, subID string)
		wantAmount  int64
	}{
		{"add-on paid", "00000000-0000-0000-0000-00000000c50d",
			func(t *testing.T, pool *pgxpool.Pool, user, orgID, subID string) {
				t.Helper()
				e := billing.NewWebhookModuleEngine(pool, user, orgID)
				payByWebhook(t, e, pool, requestPendingAddon(t, e, pool, subID, orgID, 5))
			}, 1200},
		{"plan change", "00000000-0000-0000-0000-00000000c50e",
			func(t *testing.T, pool *pgxpool.Pool, user, orgID, _ string) {
				t.Helper()
				upgradeToGrowth(t, pool, user, orgID)
			}, 2700},
		{"cancel and undo", "00000000-0000-0000-0000-00000000c50f",
			func(t *testing.T, pool *pgxpool.Pool, user, orgID, subID string) {
				t.Helper()
				e := billing.NewModuleEngineWithRealRLS(pool, user, orgID, stubRefReader{})
				for _, step := range []struct{ path, body string }{
					{"/cancel", `{"reason":"other"}`}, {"/cancel/undo", ""},
				} {
					w := httptest.NewRecorder()
					e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+step.path, step.body))
					if w.Code != http.StatusOK {
						t.Fatalf("%s: want 200, got %d: %s", step.path, w.Code, w.Body)
					}
				}
				runAutoInvoice(t, pool, subID, orgID)
			}, 700},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testPoolBilling(t)
			user := "integ_stale_coupon_" + tc.orgID[len(tc.orgID)-3:]
			code := "STALE_ONCE_" + tc.orgID[len(tc.orgID)-3:]
			setupBillingTest(t, pool, tc.orgID)
			seedCoupon(t, pool, code, couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(200), Cadence: "once"})
			subID := provisionActive(t, pool, tc.orgID, user, "2 days")
			redeem(t, pool, user, tc.orgID, code)
			renewal := issueRenewal(t, pool, subID, tc.orgID)
			if got := invoiceAmount(t, pool, renewal); got != 700 {
				t.Fatalf("renewal: want 700, got %d", got)
			}

			tc.act(t, pool, user, tc.orgID, subID)

			pending := pendingInvoiceIDs(t, pool, subID, "subscription")
			if len(pending) != 1 {
				t.Fatalf("want one renewal pending, got %d", len(pending))
			}
			if got := invoiceAmount(t, pool, pending[0]); got != tc.wantAmount {
				t.Errorf("replacement: want %d with the discount kept, got %d", tc.wantAmount, got)
			}
			if got := couponApplied(t, pool, code, subID); got != 1 {
				t.Errorf("coupon applications: want 1, got %d", got)
			}
		})
	}
}

// A repeated add-on request voids the earlier invoice, whose discount goes back.
func TestIntegration_AddonRerequest_KeepsOnceCoupon(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_coupon_rerequest"
		orgID = "00000000-0000-0000-0000-00000000c510"
		code  = "STALE_ONCE_C510"
	)
	setupBillingTest(t, pool, orgID)
	percent := int16(10)
	seedCoupon(t, pool, code, couponSeedOpts{DiscountType: "percent", PercentOff: &percent, Cadence: "once"})
	subID := provisionActive(t, pool, orgID, user, "20 days")
	redeem(t, pool, user, orgID, code)
	e := billing.NewWebhookModuleEngine(pool, user, orgID)

	first := requestPendingAddon(t, e, pool, subID, orgID, 5)
	second := requestPendingAddon(t, e, pool, subID, orgID, 6)

	if first == second {
		t.Fatal("want the second request to reissue")
	}
	if got := couponApplied(t, pool, code, subID); got != 1 {
		t.Errorf("coupon applications: want 1, got %d", got)
	}
	var discountLines int
	if err := pool.QueryRow(t.Context(), `
		SELECT COUNT(*) FROM billing.invoice_line_items WHERE invoice_id = $1 AND total_cents < 0`, second).Scan(&discountLines); err != nil {
		t.Fatalf("read line items: %v", err)
	}
	if discountLines != 1 {
		t.Errorf("second invoice: want the discount kept, got %d discount lines", discountLines)
	}
}

// A late payment reactivating the subscription leaves a Resume invoice with nothing
// to pay for. Left open, its checkout's expiry would past-due the fresh period.
func TestIntegration_LatePayment_VoidsPendingResumeInvoice(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_late_resume"
		orgID = "00000000-0000-0000-0000-00000000c511"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "2 days")
	renewal := issueRenewal(t, pool, subID, orgID)
	seedLinkWithStatus(t, pool, renewal, "cs_c511_old", "expired")
	if _, err := pool.Exec(t.Context(), `
		UPDATE billing.subscriptions SET status = 'expired', period_end = now() - interval '1 hour' WHERE id = $1`, subID); err != nil {
		t.Fatalf("end the subscription: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE billing.invoices SET status = 'failed' WHERE id = $1`, renewal); err != nil {
		t.Fatalf("fail the renewal: %v", err)
	}
	e := billing.NewWebhookModuleEngine(pool, user, orgID)
	useProviderTransport(t, http.StatusInternalServerError)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/resume", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("resume: want 200, got %d: %s", w.Code, w.Body)
	}
	resume := pendingInvoiceIDs(t, pool, subID, "subscription")
	if len(resume) != 1 {
		t.Fatalf("want one Resume invoice, got %d", len(resume))
	}

	postPaidStripeWebhook(t, e, "cs_c511_old")

	if got := subscriptionStatus(t, pool, subID); got != "active" {
		t.Fatalf("status: want active, got %q", got)
	}
	if got := invoiceStatus(t, pool, resume[0]); got != "void" {
		t.Errorf("Resume invoice: want void, got %q", got)
	}
}

// A decline carries the invoice and, since this was added, the checkout it came
// from. One from a checkout already replaced must leave the open one alone.
func TestIntegration_StripeDecline_FromReplacedCheckout_IsIgnored(t *testing.T) {
	cases := []struct {
		name, orgID                 string
		fromCurrent                 bool
		wantStatus, wantCurrentLink string
	}{
		{"replaced checkout", "00000000-0000-0000-0000-00000000c512", false, "active", "pending"},
		{"current checkout", "00000000-0000-0000-0000-00000000c513", true, "past_due", "failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testPoolBilling(t)
			user := "integ_stale_decline_" + tc.orgID[len(tc.orgID)-3:]
			setupBillingTest(t, pool, tc.orgID)
			subID := provisionActive(t, pool, tc.orgID, user, "2 days")
			inv := seedInvoice(pool, subID, "USD", 900)
			old := seedPaymentLink(pool, inv, "cs_old_"+tc.orgID, "stripe", "USD", 900)
			if _, err := pool.Exec(t.Context(), `UPDATE billing.payment_links SET status = 'expired' WHERE id = $1`, old); err != nil {
				t.Fatalf("expire old link: %v", err)
			}
			current := seedPaymentLink(pool, inv, "cs_new_"+tc.orgID, "stripe", "USD", 900)

			from := old
			if tc.fromCurrent {
				from = current
			}
			payload := fmt.Sprintf(
				`{"type":"payment_intent.payment_failed","data":{"object":{"id":"pi_x","metadata":{"invoice_id":%q,"payment_link_id":%q}}}}`, inv, from)
			w := httptest.NewRecorder()
			billing.NewWebhookModuleEngine(pool, user, tc.orgID).ServeHTTP(w,
				httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
			if w.Code != http.StatusOK {
				t.Fatalf("webhook: want 200, got %d: %s", w.Code, w.Body)
			}

			if got := subscriptionStatus(t, pool, subID); got != tc.wantStatus {
				t.Errorf("status: want %q, got %q", tc.wantStatus, got)
			}
			var linkStatus string
			pool.QueryRow(t.Context(), `SELECT status FROM billing.payment_links WHERE id = $1`, current).Scan(&linkStatus)
			if linkStatus != tc.wantCurrentLink {
				t.Errorf("current link: want %q, got %q", tc.wantCurrentLink, linkStatus)
			}
		})
	}
}

// The link id only helps if the checkout really carries it.
func TestIntegration_CreatePaymentLink_SendsItsIDToStripe(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_link_metadata"
		orgID = "00000000-0000-0000-0000-00000000c514"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "2 days")
	inv := seedInvoice(pool, subID, "USD", 900)

	rt := useStripeCheckoutTransport(t)
	mod := billing.New(pool, billing.ProviderConfig{StripeAPIKey: "sk_test_metadata"}, nil, nil, nil)
	if err := mod.CreatePaymentLinkForTest(t.Context(), inv); err != nil {
		t.Fatalf("create link: %v", err)
	}

	var id string
	if err := pool.QueryRow(t.Context(), `SELECT id FROM billing.payment_links WHERE invoice_id = $1`, inv).Scan(&id); err != nil {
		t.Fatalf("read link: %v", err)
	}
	if got := rt.form.Get("payment_intent_data[metadata][payment_link_id]"); got != id {
		t.Errorf("metadata: want the link's id %q, got %q", id, got)
	}
	if got := rt.form.Get("payment_intent_data[metadata][invoice_id]"); got != inv {
		t.Errorf("metadata: want the invoice id %q, got %q", inv, got)
	}
}

// stripeCheckoutTransport stands in for Stripe's Checkout API and keeps the form it
// was sent.
type stripeCheckoutTransport struct{ form url.Values }

func (rt *stripeCheckoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if rt.form, err = url.ParseQuery(string(body)); err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK, Header: make(http.Header), Request: req,
		Body: io.NopCloser(strings.NewReader(`{"id":"cs_c514","url":"https://pay.example.com/c514"}`)),
	}, nil
}

func useStripeCheckoutTransport(t *testing.T) *stripeCheckoutTransport {
	t.Helper()
	rt := &stripeCheckoutTransport{}
	prev := httpclient.TrustedClient.Transport
	httpclient.TrustedClient.Transport = rt
	t.Cleanup(func() { httpclient.TrustedClient.Transport = prev })
	return rt
}

// --- A charge a coupon covers in full ---

// seedFullCoupon gives the test a coupon that absorbs any amount.
func seedFullCoupon(t *testing.T, pool *pgxpool.Pool, code, cadence string) {
	t.Helper()
	percent := int16(100)
	opts := couponSeedOpts{DiscountType: "percent", PercentOff: &percent, Cadence: cadence}
	if cadence == "repeated" {
		opts.DurationCount = new(3)
	}
	seedCoupon(t, pool, code, opts)
}

func TestIntegration_CoveredCharge_Provisioning(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_covered_provision"
		first = "00000000-0000-0000-0000-00000000c520"
		org   = "00000000-0000-0000-0000-00000000c521"
		code  = "COVERED_C521"
	)
	cleanupBillingByOrganization(pool, first)
	cleanupBillingByOrganization(pool, org)
	t.Cleanup(func() {
		cleanupBillingByOrganization(pool, first)
		cleanupBillingByOrganization(pool, org)
	})
	seedBillingOrganization(pool, first, user)
	seedBillingOrganization(pool, org, user)
	seedFullCoupon(t, pool, code, "once")

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(organization.New(pool, testWebhookEncryptionKey, "", 1))
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(first, user)); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	event := encodeOrganizationCreatedEventWithCart(org, user, "solo", "monthly", nil, code)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), event); err != nil {
		t.Fatalf("second provision: %v", err)
	}
	subID := getSubscriptionID(pool, org)
	if got := invoiceCount(t, pool, subID); got != 0 {
		t.Errorf("want no invoice for a covered activation, got %d", got)
	}
	if got := couponApplied(t, pool, code, subID); got != 1 {
		t.Errorf("coupon applications: want 1, got %d", got)
	}
	if got := subscriptionStatus(t, pool, subID); got != "active" {
		t.Errorf("status: want active, got %q", got)
	}

	// A redelivery finds the once coupon used up. It must not bill the activation
	// that coupon already covered.
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), event); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if got := invoiceCount(t, pool, subID); got != 0 {
		t.Errorf("after a redelivery: want no invoice, got %d", got)
	}
	if got := couponApplied(t, pool, code, subID); got != 1 {
		t.Errorf("after a redelivery: coupon applications want 1, got %d", got)
	}
}

func TestIntegration_CoveredCharge_ActivateNow(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_covered_activate"
		orgID = "00000000-0000-0000-0000-00000000c522"
		code  = "COVERED_C522"
	)
	setupBillingTest(t, pool, orgID)
	seedFullCoupon(t, pool, code, "once")
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	redeem(t, pool, user, orgID, code)

	w := httptest.NewRecorder()
	billing.NewWebhookModuleEngine(pool, user, orgID).ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/activate", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("activate: want 200, got %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Data *struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data != nil {
		t.Errorf("want no invoice in the answer, got %q", resp.Data.ID)
	}
	if got := subscriptionStatus(t, pool, subID); got != "active" {
		t.Errorf("status: want active, got %q", got)
	}
	if got := invoiceCount(t, pool, subID); got != 0 {
		t.Errorf("want no invoice, got %d", got)
	}
	if got := couponApplied(t, pool, code, subID); got != 1 {
		t.Errorf("coupon applications: want 1, got %d", got)
	}
	start, end := periodOf(t, pool, subID)
	if days := end.Sub(start).Hours() / 24; days < 28 {
		t.Errorf("period: want a full cycle, got %.1f days", days)
	}
}

func TestIntegration_CoveredCharge_Resume(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_covered_resume"
		orgID = "00000000-0000-0000-0000-00000000c523"
		code  = "COVERED_C523"
	)
	setupBillingTest(t, pool, orgID)
	seedFullCoupon(t, pool, code, "once")
	subID := provisionActive(t, pool, orgID, user, "2 days")
	if _, err := pool.Exec(t.Context(), `
		UPDATE billing.subscriptions SET status = 'expired', period_end = now() - interval '1 hour' WHERE id = $1`, subID); err != nil {
		t.Fatalf("end the subscription: %v", err)
	}
	redeem(t, pool, user, orgID, code)
	checksBefore := len(queuedPayloads(t, pool, events.RoutingKeySubscriptionCheck, orgID))

	w := httptest.NewRecorder()
	suspender := &stubOrgSuspender{}
	billing.NewWebhookModuleEngineWithSuspender(pool, user, orgID, suspender).ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/resume", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("resume: want 200, got %d: %s", w.Code, w.Body)
	}
	if got := getJSONStatus(t, w); got != "active" {
		t.Errorf("answer: want the reactivated subscription, got status %q", got)
	}
	if got := subscriptionStatus(t, pool, subID); got != "active" {
		t.Errorf("status: want active, got %q", got)
	}
	if got := invoiceCount(t, pool, subID); got != 0 {
		t.Errorf("want no invoice, got %d", got)
	}
	if got := couponApplied(t, pool, code, subID); got != 1 {
		t.Errorf("coupon applications: want 1, got %d", got)
	}
	if _, end := periodOf(t, pool, subID); !end.After(time.Now().AddDate(0, 0, 27)) {
		t.Errorf("period: want a fresh cycle, got end %s", end)
	}
	if got := len(queuedPayloads(t, pool, events.RoutingKeySubscriptionCheck, orgID)); got <= checksBefore {
		t.Errorf("want the next period's expiry check queued, had %d, now %d", checksBefore, got)
	}
	if len(suspender.unsuspended) != 1 {
		t.Errorf("want billing's suspension lifted once, got %d", len(suspender.unsuspended))
	}
}

func getJSONStatus(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.Data.Status
}

// A coupon that leaves nothing to pay must renew, not let the subscription lapse.
func TestIntegration_CoveredCharge_Renewal(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_covered_renewal"
		orgID = "00000000-0000-0000-0000-00000000c524"
		code  = "COVERED_C524"
	)
	setupBillingTest(t, pool, orgID)
	seedFullCoupon(t, pool, code, "repeated")
	subID := provisionActive(t, pool, orgID, user, "2 days")
	redeem(t, pool, user, orgID, code)
	_, endBefore := periodOf(t, pool, subID)
	check := encodeSubscriptionCheckEvent(subID, orgID, endBefore)
	mod := billing.NewModuleForTest(pool, stubRefReader{})

	if err := mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), check); err != nil {
		t.Fatalf("auto-invoice: %v", err)
	}
	if got := invoiceCount(t, pool, subID); got != 0 {
		t.Errorf("want no invoice, got %d", got)
	}
	_, end := periodOf(t, pool, subID)
	if want := endBefore.AddDate(0, 1, 0); !end.Equal(want) {
		t.Errorf("period_end: want %s, got %s", want, end)
	}
	if got := couponApplied(t, pool, code, subID); got != 1 {
		t.Errorf("coupon applications: want 1, got %d", got)
	}

	// The same message again reads as stale, so the period rolls only once.
	if err := mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), check); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if _, again := periodOf(t, pool, subID); !again.Equal(end) {
		t.Errorf("a redelivery moved period_end to %s", again)
	}
	if got := couponApplied(t, pool, code, subID); got != 1 {
		t.Errorf("after a redelivery: coupon applications want 1, got %d", got)
	}
}

// Nothing left to pay on the reissue means the activation is settled, and the
// longer cycle the change picked is granted the way a payment would grant it.
func TestIntegration_CoveredCharge_PlanChangeOnUnpaidActivation(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_covered_plan_activation"
		orgID = "00000000-0000-0000-0000-00000000c525"
		code  = "COVERED_C525"
	)
	subID, activation := seedUnpaidActivation(t, pool, orgID, user)
	seedFullCoupon(t, pool, code, "once")
	redeem(t, pool, user, orgID, code)

	changePlanTo(t, pool, user, orgID, "solo", "yearly")

	if got := invoiceStatus(t, pool, activation); got != "void" {
		t.Errorf("old activation: want void, got %q", got)
	}
	if got := pendingInvoiceIDs(t, pool, subID, "activation"); len(got) != 0 {
		t.Errorf("want nothing left to pay, got %v", got)
	}
	if got := couponApplied(t, pool, code, subID); got != 1 {
		t.Errorf("coupon applications: want 1, got %d", got)
	}
	start, end := periodOf(t, pool, subID)
	if want := start.AddDate(1, 0, 0); !end.Equal(want) {
		t.Errorf("period_end: want a full year from the start, %s, got %s", want, end)
	}
}

// The renewal voided by a plan change has nothing to charge. The auto-invoice rolls
// the period when it comes due, and it is the one that uses the coupon.
func TestIntegration_CoveredCharge_PlanChangeOnPendingRenewal(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_covered_plan_renewal"
		orgID = "00000000-0000-0000-0000-00000000c526"
		code  = "COVERED_C526"
	)
	setupBillingTest(t, pool, orgID)
	seedFullCoupon(t, pool, code, "once")
	subID := provisionActive(t, pool, orgID, user, "2 days")
	renewal := issueRenewal(t, pool, subID, orgID)
	redeem(t, pool, user, orgID, code)
	queuedBefore := len(queuedPayloads(t, pool, events.RoutingKeySubscriptionAutoInvoice, orgID))

	changePlanTo(t, pool, user, orgID, "solo", "yearly")

	if got := invoiceStatus(t, pool, renewal); got != "void" {
		t.Errorf("old renewal: want void, got %q", got)
	}
	if got := pendingInvoiceIDs(t, pool, subID, "subscription"); len(got) != 0 {
		t.Errorf("want no renewal pending, got %v", got)
	}
	if got := couponApplied(t, pool, code, subID); got != 0 {
		t.Errorf("coupon applications: want 0 until the renewal runs, got %d", got)
	}
	if got := len(queuedPayloads(t, pool, events.RoutingKeySubscriptionAutoInvoice, orgID)); got <= queuedBefore {
		t.Errorf("want the auto-invoice queued again, had %d, now %d", queuedBefore, got)
	}
}

// --- A plan nobody has priced ---

func TestIntegration_ZeroPricedPlan_NoCoupon_Refused(t *testing.T) {
	cases := []struct {
		name, orgID, path, body, wantCode string
		prepare                           func(t *testing.T, pool *pgxpool.Pool, subID string)
	}{
		{"activate-now on a trial", "00000000-0000-0000-0000-00000000c530", "/activate", "", "PLAN_NOT_SELF_SERVICE", nil},
		{"resume", "00000000-0000-0000-0000-00000000c531", "/resume", "", "PLAN_NOT_SELF_SERVICE",
			func(t *testing.T, pool *pgxpool.Pool, subID string) {
				t.Helper()
				if _, err := pool.Exec(t.Context(), `
					UPDATE billing.subscriptions SET status = 'expired', trial_end = NULL, period_end = now() - interval '1 hour' WHERE id = $1`, subID); err != nil {
					t.Fatalf("end the subscription: %v", err)
				}
			}},
		{"extend", "00000000-0000-0000-0000-00000000c532", "/extend", `{"months":1}`, "SUBSCRIPTION_NOT_EXTENDABLE",
			func(t *testing.T, pool *pgxpool.Pool, subID string) {
				t.Helper()
				forceActive(t, pool, subID, "20 days")
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testPoolBilling(t)
			user := "integ_zero_priced_" + tc.orgID[len(tc.orgID)-3:]
			setupBillingTest(t, pool, tc.orgID)
			mod := billing.NewModuleForTest(pool, stubRefReader{})
			if err := mod.Worker.HandleOrganizationCreated(t.Context(),
				encodeOrganizationCreatedEventWithPlan(tc.orgID, user, "custom", "monthly")); err != nil {
				t.Fatalf("provision: %v", err)
			}
			subID := getSubscriptionID(pool, tc.orgID)
			if tc.prepare != nil {
				tc.prepare(t, pool, subID)
			}

			w := httptest.NewRecorder()
			billing.NewWebhookModuleEngine(pool, user, tc.orgID).ServeHTTP(w,
				httpserver.JSONTestRequest(http.MethodPost, billingURL(tc.orgID)+tc.path, tc.body))
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("want 422, got %d: %s", w.Code, w.Body)
			}
			if got := responseErrorCode(t, w); got != tc.wantCode {
				t.Errorf("code: want %s, got %s", tc.wantCode, got)
			}
			if got := invoiceCount(t, pool, subID); got != 0 {
				t.Errorf("want no invoice, got %d", got)
			}
		})
	}
}

// --- Which plan an owner can pick ---

// Every status goes through /downgrade for a lower plan, which resolves overage,
// so PATCH never needs to accept one.
func TestIntegration_ChangePlan_LowerTier_RefusedOnEveryStatus(t *testing.T) {
	for i, status := range []string{"active", "trialing", "past_due", "expired"} {
		t.Run(status, func(t *testing.T) {
			pool := testPoolBilling(t)
			orgID := "00000000-0000-0000-0000-00000000c54" + strconv.Itoa(i)
			user := "integ_patch_lower_" + status
			setupBillingTest(t, pool, orgID)
			subID := provisionActive(t, pool, orgID, user, "20 days")
			if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET plan = 'growth', status = $2 WHERE id = $1`, subID, status); err != nil {
				t.Fatalf("set fixture: %v", err)
			}

			w := httptest.NewRecorder()
			billing.NewModuleEngineWithRealRLS(pool, user, orgID, stubRefReader{}).ServeHTTP(w, httpserver.JSONTestRequest(
				http.MethodPatch, billingURL(orgID)+"/plan", `{"plan":"solo","cycle":"monthly","terms_agreed":true}`))
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("want 422, got %d: %s", w.Code, w.Body)
			}
			if got := responseErrorCode(t, w); got != "PLAN_CHANGE_IS_DOWNGRADE" {
				t.Errorf("code: want PLAN_CHANGE_IS_DOWNGRADE, got %s", got)
			}
			var plan string
			pool.QueryRow(t.Context(), `SELECT plan FROM billing.subscriptions WHERE id = $1`, subID).Scan(&plan)
			if plan != "growth" {
				t.Errorf("plan: want growth unchanged, got %q", plan)
			}
		})
	}
}

// seedLowPlan adds a plan below every other tier, at a monthly and yearly price.
func seedLowPlan(t *testing.T, pool *pgxpool.Pool, id string, price int, active bool) {
	t.Helper()
	prices := fmt.Sprintf(`{"USD":{"monthly":%[1]d,"yearly":%[1]d},"IDR":{"monthly":%[1]d,"yearly":%[1]d}}`, price)
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO billing.plans (id, name, description, prices, sort_order, active)
		VALUES ($1, $1, $1, $2, 0, $3)`, id, prices, active); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing.plans WHERE id = $1`, id)
	})
}

func TestIntegration_PlanSelection_NotSelfServe_Refused(t *testing.T) {
	cases := []struct {
		name, orgID, method, path, body, wantCode string
	}{
		{"PATCH to a zero-priced plan", "00000000-0000-0000-0000-00000000c550", http.MethodPatch, "/plan",
			`{"plan":"custom","cycle":"monthly","terms_agreed":true}`, "PLAN_NOT_SELF_SERVICE"},
		{"PATCH to a plan no longer on sale", "00000000-0000-0000-0000-00000000c551", http.MethodPatch, "/plan",
			`{"plan":"retired_c55","cycle":"monthly","terms_agreed":true}`, "UNKNOWN_PLAN"},
		{"downgrade to a plan no longer on sale", "00000000-0000-0000-0000-00000000c552", http.MethodPost, "/downgrade",
			`{"plan":"retired_c55","cycle":"monthly"}`, "UNKNOWN_PLAN"},
		{"downgrade to a plan with no price", "00000000-0000-0000-0000-00000000c554", http.MethodPost, "/downgrade",
			`{"plan":"free_c55","cycle":"monthly"}`, "PLAN_NOT_SELF_SERVICE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testPoolBilling(t)
			user := "integ_not_self_serve_" + tc.orgID[len(tc.orgID)-3:]
			setupBillingTest(t, pool, tc.orgID)
			seedLowPlan(t, pool, "retired_c55", 100, false)
			seedLowPlan(t, pool, "free_c55", 0, true)
			subID := provisionActive(t, pool, tc.orgID, user, "20 days")
			if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET plan = 'growth' WHERE id = $1`, subID); err != nil {
				t.Fatalf("set plan: %v", err)
			}
			w := httptest.NewRecorder()
			billing.NewModuleEngineWithRealRLS(pool, user, tc.orgID, stubRefReader{}).ServeHTTP(w,
				httpserver.JSONTestRequest(tc.method, billingURL(tc.orgID)+tc.path, tc.body))
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("want 422, got %d: %s", w.Code, w.Body)
			}
			if got := responseErrorCode(t, w); got != tc.wantCode {
				t.Errorf("code: want %s, got %s", tc.wantCode, got)
			}
			var plan string
			var scheduled *string
			pool.QueryRow(t.Context(), `SELECT plan, scheduled_plan FROM billing.subscriptions WHERE id = $1`, subID).Scan(&plan, &scheduled)
			if plan != "growth" || scheduled != nil {
				t.Errorf("want nothing changed, got plan=%q scheduled=%v", plan, scheduled)
			}
		})
	}
}

// A customer an operator put on a plan that is not for sale keeps the plan, and can
// still move between its cycles.
func TestIntegration_ChangePlan_CycleSwitchOnANonSelfServePlan_Allowed(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_custom_cycle_switch"
		orgID = "00000000-0000-0000-0000-00000000c553"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "20 days")
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET plan = 'custom' WHERE id = $1`, subID); err != nil {
		t.Fatalf("set plan: %v", err)
	}

	changePlanTo(t, pool, user, orgID, "custom", "yearly")

	var cycle string
	pool.QueryRow(t.Context(), `SELECT cycle FROM billing.subscriptions WHERE id = $1`, subID).Scan(&cycle)
	if cycle != "yearly" {
		t.Errorf("cycle: want yearly, got %q", cycle)
	}
}

// Reactivating reads the add-ons live at that moment, so a late payment for an invoice
// priced before an increase would buy the raised quantity at the old price. An ended
// subscription makes this reachable: an increase prorates to nothing and applies at once.
func TestIntegration_LatePayment_AfterAddonRaised_IsNotApplied(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_late_addon"
		orgID = "00000000-0000-0000-0000-00000000c516"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "2 days")
	renewal := issueRenewal(t, pool, subID, orgID)
	seedLinkWithStatus(t, pool, renewal, "cs_c516", "expired")
	if _, err := pool.Exec(t.Context(), `
		UPDATE billing.subscriptions SET status = 'expired', period_end = now() - interval '1 hour' WHERE id = $1`, subID); err != nil {
		t.Fatalf("end the subscription: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE billing.invoices SET status = 'failed' WHERE id = $1`, renewal); err != nil {
		t.Fatalf("fail the renewal: %v", err)
	}
	e := billing.NewWebhookModuleEngine(pool, user, orgID)
	if w := requestAddon(t, e, orgID, 100); w.Code != http.StatusOK {
		t.Fatalf("attach: want 200, got %d: %s", w.Code, w.Body)
	}
	if got := liveSeats(t, pool, subID); got != 100 {
		t.Fatalf("seats: want 100 applied at once, got %d", got)
	}

	postPaidStripeWebhook(t, e, "cs_c516")

	if got := subscriptionStatus(t, pool, subID); got != "expired" {
		t.Errorf("status: want expired, got %q", got)
	}
	if got := invoiceStatus(t, pool, renewal); got != "failed" {
		t.Errorf("invoice: want failed, got %q", got)
	}
}

// A void gives back the coupon application, so a void invoice paid late after all has
// used it again. Otherwise a once-only coupon would discount a second invoice.
func TestIntegration_LatePayment_OnVoidInvoice_UsesItsCouponAgain(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_late_void_coupon"
		orgID = "00000000-0000-0000-0000-00000000c517"
		code  = "STALE_ONCE_C517"
	)
	setupBillingTest(t, pool, orgID)
	seedCoupon(t, pool, code, couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(200), Cadence: "once"})
	subID := provisionActive(t, pool, orgID, user, "2 days")
	redeem(t, pool, user, orgID, code)
	renewal := issueRenewal(t, pool, subID, orgID)
	seedLinkWithStatus(t, pool, renewal, "cs_c517", "pending")

	w := httptest.NewRecorder()
	billing.NewModuleEngineWithRealRLS(pool, user, orgID, stubRefReader{}).ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel", `{"reason":"other"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("cancel: want 200, got %d: %s", w.Code, w.Body)
	}
	if got := couponApplied(t, pool, code, subID); got != 0 {
		t.Fatalf("after the void: want the application given back, got %d", got)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE billing.subscriptions SET status = 'cancelled', scheduled_cancel_at = NULL, period_end = now() - interval '1 hour' WHERE id = $1`, subID); err != nil {
		t.Fatalf("end the subscription: %v", err)
	}

	postPaidStripeWebhook(t, billing.NewWebhookModuleEngine(pool, user, orgID), "cs_c517")

	if got := invoiceStatus(t, pool, renewal); got != "paid" {
		t.Fatalf("invoice: want paid, got %q", got)
	}
	if got := couponApplied(t, pool, code, subID); got != 1 {
		t.Errorf("coupon applications: want 1, got %d", got)
	}
}

// Duplicate auto-invoice messages are expected, since several paths queue one, and
// their handler can run concurrently. Two renewals for one period would both be payable.
func TestIntegration_AutoInvoice_ConcurrentDeliveries_IssueOneRenewal(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_autoinvoice_concurrent"
		orgID = "00000000-0000-0000-0000-00000000c518"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "2 days")
	useProviderTransport(t, http.StatusInternalServerError)
	end, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, end, isTrial)
	mod := billing.NewModuleForTest(pool, stubRefReader{})

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _ = mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body) })
	}
	wg.Wait()

	if got := pendingInvoiceIDs(t, pool, subID, "subscription"); len(got) != 1 {
		t.Errorf("want one renewal, got %d", len(got))
	}
}

// A plan change prorates from the period it reads. Read before a payment in flight
// commits, the period that payment bought would be overwritten and lost.
func TestIntegration_ChangePlan_WaitsForAPaymentInFlight(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_plan_in_flight"
		orgID = "00000000-0000-0000-0000-00000000c519"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "20 days")

	payment, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = payment.Rollback(context.Background()) }()
	if _, err := payment.Exec(t.Context(), `
		UPDATE billing.subscriptions SET period_end = period_end + interval '1 month' WHERE id = $1`, subID); err != nil {
		t.Fatalf("roll the period: %v", err)
	}

	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		billing.NewModuleEngineWithRealRLS(pool, user, orgID, stubRefReader{}).ServeHTTP(w,
			httpserver.JSONTestRequest(http.MethodPatch, billingURL(orgID)+"/plan",
				`{"plan":"growth","cycle":"monthly","terms_agreed":true}`))
		done <- w.Code
	}()
	waitForLockWaiter(t, pool, "subscriptions")
	if err := payment.Commit(t.Context()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if code := <-done; code != http.StatusOK {
		t.Fatalf("change plan: want 200, got %d", code)
	}

	// About 50 days left at 900 become about 16 at 2900. From the 20 days read before
	// the payment committed, they would be about 6.
	if _, end := periodOf(t, pool, subID); end.Before(time.Now().AddDate(0, 0, 12)) {
		t.Errorf("period_end %s was prorated from the period before the payment", end)
	}
}

// Two requests side by side must not both pass the check for a pending invoice and
// both issue one, or the owner can end up paying for the same thing twice.
func TestIntegration_ConcurrentRequests_IssueOneInvoice(t *testing.T) {
	cases := []struct {
		name, orgID, path, body, kind string
		active                        bool
	}{
		{"extend", "00000000-0000-0000-0000-00000000c51a", "/extend", `{"months":1}`, "extension", true},
		{"activate-now", "00000000-0000-0000-0000-00000000c51b", "/activate", "", "activation", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testPoolBilling(t)
			user := "integ_stale_concurrent_" + tc.orgID[len(tc.orgID)-3:]
			setupBillingTest(t, pool, tc.orgID)
			mod := billing.NewModuleForTest(pool, stubRefReader{})
			if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(tc.orgID, user)); err != nil {
				t.Fatalf("provision: %v", err)
			}
			subID := getSubscriptionID(pool, tc.orgID)
			if tc.active {
				forceActive(t, pool, subID, "20 days")
			}
			useProviderTransport(t, http.StatusInternalServerError)
			e := billing.NewWebhookModuleEngine(pool, user, tc.orgID)

			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, billingURL(tc.orgID)+tc.path, tc.body))
				})
			}
			wg.Wait()

			if got := pendingInvoiceIDs(t, pool, subID, tc.kind); len(got) != 1 {
				t.Errorf("want one %s invoice, got %d", tc.kind, len(got))
			}
		})
	}
}

// An invoice must be composed under the same lock an add-on change takes, or a request
// racing it issues an invoice priced without the add-on it then carries for a cycle.
func TestIntegration_InvoiceRequests_WaitForAnAddonChangeInFlight(t *testing.T) {
	cases := []struct {
		name, orgID, path, body, kind, prepare string
		want                                   int64
	}{
		{"activate-now", "00000000-0000-0000-0000-00000000c51c", "/activate", "", "activation", "", 1400},
		{"resume", "00000000-0000-0000-0000-00000000c51d", "/resume", "", "subscription",
			`UPDATE billing.subscriptions SET status = 'expired', trial_end = NULL, period_end = now() - interval '1 hour' WHERE id = $1`, 1400},
		{"extend", "00000000-0000-0000-0000-00000000c51e", "/extend", `{"months":1}`, "extension",
			`UPDATE billing.subscriptions SET status = 'active', trial_end = NULL, period_start = now() - interval '1 day', period_end = now() + interval '20 days' WHERE id = $1`, 1400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testPoolBilling(t)
			user := "integ_stale_in_flight_" + tc.orgID[len(tc.orgID)-3:]
			setupBillingTest(t, pool, tc.orgID)
			mod := billing.NewModuleForTest(pool, stubRefReader{})
			if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(tc.orgID, user)); err != nil {
				t.Fatalf("provision: %v", err)
			}
			subID := getSubscriptionID(pool, tc.orgID)
			if tc.prepare != "" {
				if _, err := pool.Exec(t.Context(), tc.prepare, subID); err != nil {
					t.Fatalf("prepare: %v", err)
				}
			}
			useProviderTransport(t, http.StatusInternalServerError)

			change, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = change.Rollback(context.Background()) }()
			if _, err := change.Exec(t.Context(), `SELECT id FROM billing.subscriptions WHERE id = $1 FOR UPDATE`, subID); err != nil {
				t.Fatalf("lock: %v", err)
			}
			if _, err := change.Exec(t.Context(), `
				INSERT INTO billing.subscription_addons (subscription_id, addon_id, quantity) VALUES ($1, 'extra-seat', 5)`, subID); err != nil {
				t.Fatalf("raise the add-on: %v", err)
			}

			done := make(chan int, 1)
			e := billing.NewWebhookModuleEngine(pool, user, tc.orgID)
			go func() {
				w := httptest.NewRecorder()
				e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(tc.orgID)+tc.path, tc.body))
				done <- w.Code
			}()
			waitForLockWaiter(t, pool, "subscriptions")
			if err := change.Commit(t.Context()); err != nil {
				t.Fatalf("commit: %v", err)
			}
			if code := <-done; code != http.StatusOK {
				t.Fatalf("%s: want 200, got %d", tc.path, code)
			}

			pending := pendingInvoiceIDs(t, pool, subID, tc.kind)
			if len(pending) != 1 {
				t.Fatalf("want one %s invoice, got %d", tc.kind, len(pending))
			}
			if got := invoiceAmount(t, pool, pending[0]); got != tc.want {
				t.Errorf("amount: want %d with the add-on, got %d", tc.want, got)
			}
		})
	}
}

// A coupon absorbing an increase must be used by it, or a once-only coupon pays for one
// increase after another and is still there for the renewal.
func TestIntegration_AddonIncrease_CoveredByCoupon_UsesIt(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_addon_coupon_used"
		orgID = "00000000-0000-0000-0000-00000000c560"
		code  = "STALE_C560"
	)
	setupBillingTest(t, pool, orgID)
	seedFullCoupon(t, pool, code, "once")
	subID := provisionActive(t, pool, orgID, user, "20 days")
	redeem(t, pool, user, orgID, code)
	e := billing.NewWebhookModuleEngine(pool, user, orgID)

	if w := requestAddon(t, e, orgID, 5); w.Code != http.StatusOK {
		t.Fatalf("first attach: want 200, got %d: %s", w.Code, w.Body)
	}
	if got := liveSeats(t, pool, subID); got != 5 {
		t.Fatalf("seats: want the covered increase applied, got %d", got)
	}
	if got := couponApplied(t, pool, code, subID); got != 1 {
		t.Errorf("coupon applications: want 1, got %d", got)
	}

	second := requestPendingAddon(t, e, pool, subID, orgID, 6)
	if got := invoiceAmount(t, pool, second); got <= 0 {
		t.Errorf("second increase: want it charged, got %d", got)
	}
	if got := liveSeats(t, pool, subID); got != 5 {
		t.Errorf("seats: want 5 until the second increase is paid, got %d", got)
	}
}

// A fixed amount is off an invoice, and an increase can be split into as many small
// invoices as the owner likes. Each would be absorbed in full, seat by seat.
func TestIntegration_AddonIncrease_FixedCoupon_DoesNotApply(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_addon_fixed_coupon"
		orgID = "00000000-0000-0000-0000-00000000c561"
		code  = "STALE_C561"
	)
	setupBillingTest(t, pool, orgID)
	seedCoupon(t, pool, code, couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(5000), Cadence: "forever"})
	subID := provisionActive(t, pool, orgID, user, "20 days")
	redeem(t, pool, user, orgID, code)
	e := billing.NewWebhookModuleEngine(pool, user, orgID)

	inv := requestPendingAddon(t, e, pool, subID, orgID, 1)

	if got := invoiceAmount(t, pool, inv); got < 90 || got > 100 {
		t.Errorf("amount: want about 95, the seat for the days left, got %d", got)
	}
	if got := liveSeats(t, pool, subID); got != 0 {
		t.Errorf("seats: want none until paid, got %d", got)
	}
	if got := couponApplied(t, pool, code, subID); got != 0 {
		t.Errorf("coupon applications: want 0, got %d", got)
	}
}

// Skipping the rest of a trial replaces the invoice that would have converted it: both
// paying would buy two cycles, and either left open would dun or block a paid org.
func TestIntegration_ActivateNow_ReplacesTheConversionInvoice(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_activate_conversion"
		orgID = "00000000-0000-0000-0000-00000000c562"
		code  = "STALE_C562"
	)
	setupBillingTest(t, pool, orgID)
	seedCoupon(t, pool, code, couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(200), Cadence: "once"})
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	redeem(t, pool, user, orgID, code)
	conversion := issueRenewal(t, pool, subID, orgID)
	seedLinkWithStatus(t, pool, conversion, "cs_c562", "pending")
	useProviderTransport(t, http.StatusInternalServerError)

	w := httptest.NewRecorder()
	billing.NewWebhookModuleEngine(pool, user, orgID).ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/activate", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("activate: want 200, got %d: %s", w.Code, w.Body)
	}

	if got := invoiceStatus(t, pool, conversion); got != "void" {
		t.Errorf("conversion invoice: want void, got %q", got)
	}
	if got := pendingInvoiceIDs(t, pool, subID, "subscription"); len(got) != 0 {
		t.Errorf("want no renewal left pending, got %v", got)
	}
	activation := pendingInvoiceIDs(t, pool, subID, "activation")
	if len(activation) != 1 {
		t.Fatalf("want one activation, got %d", len(activation))
	}
	if got := invoiceAmount(t, pool, activation[0]); got != 700 {
		t.Errorf("activation: want 700 with the discount kept, got %d", got)
	}
	if got := couponApplied(t, pool, code, subID); got != 1 {
		t.Errorf("coupon applications: want 1, got %d", got)
	}
}

// The auto-invoice ignores a past-due subscription, so queueing it would void the
// renewal and leave nothing behind. A past-due renewal is already due: it settles here.
func TestIntegration_CoveredCharge_RepriceOnPastDue_RenewsAtOnce(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_covered_past_due"
		orgID = "00000000-0000-0000-0000-00000000c563"
		code  = "COVERED_C563"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "2 days")
	renewal := issueRenewal(t, pool, subID, orgID)
	seedFullCoupon(t, pool, code, "forever")
	redeem(t, pool, user, orgID, code)
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET status = 'past_due' WHERE id = $1`, subID); err != nil {
		t.Fatalf("set status: %v", err)
	}
	_, endBefore := periodOf(t, pool, subID)

	if w := requestAddon(t, billing.NewWebhookModuleEngine(pool, user, orgID), orgID, 1); w.Code != http.StatusOK {
		t.Fatalf("attach: want 200, got %d: %s", w.Code, w.Body)
	}

	if got := invoiceStatus(t, pool, renewal); got != "void" {
		t.Errorf("old renewal: want void, got %q", got)
	}
	if got := subscriptionStatus(t, pool, subID); got != "active" {
		t.Errorf("status: want active, got %q", got)
	}
	if _, end := periodOf(t, pool, subID); !end.Equal(endBefore.AddDate(0, 1, 0)) {
		t.Errorf("period_end: want one more month after %s, got %s", endBefore, end)
	}
}

// A decrease or removal means the owner no longer wants that increase. Left open, it
// could be paid after the renewal removed the row, and the payment would apply to nothing.
func TestIntegration_AddonDecrease_DropsItsPendingIncrease(t *testing.T) {
	cases := []struct {
		name, orgID string
		act         func(t *testing.T, pool *pgxpool.Pool, user, orgID string)
	}{
		{"detach", "00000000-0000-0000-0000-00000000c564", func(t *testing.T, pool *pgxpool.Pool, user, orgID string) {
			t.Helper()
			w := httptest.NewRecorder()
			billing.NewModuleEngineWithRealRLS(pool, user, orgID, stubRefReader{}).ServeHTTP(w,
				httpserver.JSONTestRequest(http.MethodDelete, billingURL(orgID)+"/addons/extra-seat", ""))
			if w.Code >= http.StatusMultipleChoices {
				t.Fatalf("detach: got %d: %s", w.Code, w.Body)
			}
		}},
		{"decrease", "00000000-0000-0000-0000-00000000c565", func(t *testing.T, pool *pgxpool.Pool, user, orgID string) {
			t.Helper()
			if w := requestAddon(t, billing.NewWebhookModuleEngine(pool, user, orgID), orgID, 1); w.Code != http.StatusOK {
				t.Fatalf("decrease: want 200, got %d: %s", w.Code, w.Body)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testPoolBilling(t)
			user := "integ_stale_decrease_" + tc.orgID[len(tc.orgID)-3:]
			setupBillingTest(t, pool, tc.orgID)
			subID := provisionActive(t, pool, tc.orgID, user, "20 days")
			if _, err := pool.Exec(t.Context(), `
				INSERT INTO billing.subscription_addons (subscription_id, addon_id, quantity) VALUES ($1, 'extra-seat', 2)`, subID); err != nil {
				t.Fatalf("seed add-on: %v", err)
			}
			increase := requestPendingAddon(t, billing.NewWebhookModuleEngine(pool, user, tc.orgID), pool, subID, tc.orgID, 5)
			seedLinkWithStatus(t, pool, increase, "cs_"+tc.orgID, "pending")

			tc.act(t, pool, user, tc.orgID)

			if got := invoiceStatus(t, pool, increase); got != "void" {
				t.Errorf("increase invoice: want void, got %q", got)
			}
			var pendingQuantity *int
			if err := pool.QueryRow(t.Context(), `
				SELECT pending_quantity FROM billing.subscription_addons WHERE subscription_id = $1 AND addon_id = 'extra-seat'`,
				subID).Scan(&pendingQuantity); err != nil {
				t.Fatalf("read pending quantity: %v", err)
			}
			if pendingQuantity != nil {
				t.Errorf("want the pending increase cleared, got %d", *pendingQuantity)
			}
		})
	}
}

// Once used up, a once-only coupon stops being the active redemption. Redeeming it
// again must not start a fresh one.
func TestIntegration_RedeemCoupon_SameCodeTwice_Refused(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_coupon_twice"
		orgID = "00000000-0000-0000-0000-00000000c566"
		code  = "STALE_C566"
	)
	setupBillingTest(t, pool, orgID)
	seedCoupon(t, pool, code, couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(200), Cadence: "once"})
	subID := provisionActive(t, pool, orgID, user, "2 days")
	redeem(t, pool, user, orgID, code)
	issueRenewal(t, pool, subID, orgID)
	if got := couponApplied(t, pool, code, subID); got != 1 {
		t.Fatalf("coupon applications: want 1, got %d", got)
	}

	w := httptest.NewRecorder()
	billing.NewModuleEngineWithRealRLS(pool, user, orgID, stubRefReader{}).ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/coupons/redeem", `{"code":"`+code+`"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second redeem: want 422, got %d: %s", w.Code, w.Body)
	}
	var rows int
	if err := pool.QueryRow(t.Context(), `
		SELECT COUNT(*) FROM billing.coupon_redemptions WHERE coupon_id = $1 AND subscription_id = $2`, code, subID).Scan(&rows); err != nil {
		t.Fatalf("count redemptions: %v", err)
	}
	if rows != 1 {
		t.Errorf("redemptions: want 1, got %d", rows)
	}

	// Nor may the Pay dialog keep offering a code it would refuse.
	w = httptest.NewRecorder()
	billing.NewModuleEngineWithRealRLS(pool, user, orgID, stubRefReader{}).ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodGet, billingURL(orgID)+"/coupons", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("list coupons: want 200, got %d: %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), code) {
		t.Errorf("eligible coupons still list %s: %s", code, w.Body)
	}
}

// An invoice that failed when the subscription ended was never paid, so its discount was
// never used, and Resume should still get it. Paid late after all, it has used it again.
func TestIntegration_FailedInvoice_ReleasesTheCoupon(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_stale_failed_coupon"
		orgID = "00000000-0000-0000-0000-00000000c567"
		code  = "STALE_C567"
	)
	setupBillingTest(t, pool, orgID)
	seedCoupon(t, pool, code, couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(200), Cadence: "once"})
	subID := provisionActive(t, pool, orgID, user, "2 days")
	redeem(t, pool, user, orgID, code)
	renewal := issueRenewal(t, pool, subID, orgID)
	seedLinkWithStatus(t, pool, renewal, "cs_c567", "pending")
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET period_end = now() - interval '1 minute' WHERE id = $1`, subID); err != nil {
		t.Fatalf("end the period: %v", err)
	}
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	end, _ := getSubscriptionExpectedEnd(pool, subID)
	if err := mod.Worker.HandleSubscriptionCheck(t.Context(), encodeSubscriptionCheckEvent(subID, orgID, end)); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if got := invoiceStatus(t, pool, renewal); got != "failed" {
		t.Fatalf("renewal: want failed, got %q", got)
	}
	if got := couponApplied(t, pool, code, subID); got != 0 {
		t.Errorf("after failing: want the application given back, got %d", got)
	}

	postPaidStripeWebhook(t, billing.NewWebhookModuleEngine(pool, user, orgID), "cs_c567")

	if got := invoiceStatus(t, pool, renewal); got != "paid" {
		t.Fatalf("renewal: want paid late, got %q", got)
	}
	if got := couponApplied(t, pool, code, subID); got != 1 {
		t.Errorf("after the late payment: want the application taken again, got %d", got)
	}
}

// A covered activation granting the longer cycle must queue the renewal sequence once,
// or the owner gets every reminder twice.
func TestIntegration_CoveredCharge_PlanChange_SchedulesOnce(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_covered_plan_once"
		orgID = "00000000-0000-0000-0000-00000000c568"
		code  = "COVERED_C568"
	)
	seedUnpaidActivation(t, pool, orgID, user)
	seedFullCoupon(t, pool, code, "once")
	redeem(t, pool, user, orgID, code)
	before := len(queuedPayloads(t, pool, events.RoutingKeySubscriptionRemind, orgID))

	changePlanTo(t, pool, user, orgID, "solo", "yearly")

	if got := len(queuedPayloads(t, pool, events.RoutingKeySubscriptionRemind, orgID)) - before; got != 1 {
		t.Errorf("reminders queued: want 1, got %d", got)
	}
}
