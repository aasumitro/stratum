package billing_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// An expired checkout means the owner has not paid yet, not that a payment
// failed: the link closes and nothing else changes. The invoice stays
// payable through a new checkout, the subscription keeps its status, and no
// failure notice, dunning or customer webhook goes out. Whether the owner
// pays at all is settled at period_end, as for any unpaid invoice.
func TestCheckoutExpiry_IsNotAPaymentFailure(t *testing.T) {
	cases := []struct {
		name, orgID, kind, provider, currency, extID, path, body string
	}{
		{"stripe renewal", "00000000-0000-0000-0000-00000000c0e1", "subscription", "stripe", "USD", "cs_c0e1",
			"/webhooks/stripe", `{"type":"checkout.session.expired","data":{"object":{"id":"cs_c0e1"}}}`},
		{"stripe activation", "00000000-0000-0000-0000-00000000c0e2", "activation", "stripe", "USD", "cs_c0e2",
			"/webhooks/stripe", `{"type":"checkout.session.expired","data":{"object":{"id":"cs_c0e2"}}}`},
		{"xendit renewal", "00000000-0000-0000-0000-00000000c0e3", "subscription", "xendit", "IDR", "xnd_c0e3",
			"/webhooks/xendit", `{"id":"xnd_c0e3","status":"EXPIRED"}`},
		{"xendit activation", "00000000-0000-0000-0000-00000000c0e4", "activation", "xendit", "IDR", "xnd_c0e4",
			"/webhooks/xendit", `{"id":"xnd_c0e4","status":"EXPIRED"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testPoolBilling(t)
			user := "integ_checkout_expiry_" + tc.orgID[len(tc.orgID)-4:]
			setupBillingTest(t, pool, tc.orgID)
			subID := provisionActive(t, pool, tc.orgID, user, "2 days")
			if _, err := pool.Exec(t.Context(), `DELETE FROM messaging.outbox WHERE payload->>'org_id' = $1`, tc.orgID); err != nil {
				t.Fatalf("clear outbox: %v", err)
			}
			var invID string
			if err := pool.QueryRow(t.Context(), `
				INSERT INTO billing.invoices (subscription_id, amount_cents, currency, kind)
				VALUES ($1, 900, $2, $3) RETURNING id`, subID, tc.currency, tc.kind).Scan(&invID); err != nil {
				t.Fatalf("seed invoice: %v", err)
			}
			seedPaymentLink(pool, invID, tc.extID, tc.provider, tc.currency, 900)

			w := httptest.NewRecorder()
			billing.NewWebhookModuleEngine(pool, user, tc.orgID).ServeHTTP(w,
				httpserver.JSONTestRequest(http.MethodPost, tc.path, tc.body))
			if w.Code != http.StatusOK {
				t.Fatalf("webhook: want 200, got %d: %s", w.Code, w.Body)
			}

			var subStatus, invStatus string
			pool.QueryRow(t.Context(), `SELECT status FROM billing.subscriptions WHERE id = $1`, subID).Scan(&subStatus)
			pool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, invID).Scan(&invStatus)
			if subStatus != "active" {
				t.Errorf("subscription: want active, got %q", subStatus)
			}
			if invStatus != "pending" {
				t.Errorf("invoice: want pending (still payable), got %q", invStatus)
			}
			if got := linkStatuses(t, pool, invID); !slices.Equal(got, []string{"expired"}) {
				t.Errorf("link: want expired, got %v", got)
			}
			for _, key := range []string{
				events.RoutingKeyInvoiceFailed,
				events.RoutingKeySubscriptionPaymentRemind,
				events.RoutingKeySubscriptionPaymentFinal,
			} {
				if n := countOrgOutboxRows(t, pool, key, tc.orgID); n != 0 {
					t.Errorf("%s: want 0 outbox rows, got %d", key, n)
				}
			}
		})
	}
}

// A payment attempt that really failed still counts: the owner hears about it,
// dunning is scheduled, and an unpaid period-backing invoice past-dues the
// subscription. A later expiry of the same checkout only closes the link.
func TestCheckoutExpiry_AfterADecline_OnlyClosesTheLink(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_checkout_expiry_after_decline"
		orgID = "00000000-0000-0000-0000-00000000c0e5"
		extID = "cs_c0e5"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "2 days")
	if _, err := pool.Exec(t.Context(), `DELETE FROM messaging.outbox WHERE payload->>'org_id' = $1`, orgID); err != nil {
		t.Fatalf("clear outbox: %v", err)
	}
	invID := seedInvoice(pool, subID, "USD", 900)
	seedPaymentLink(pool, invID, extID, "stripe", "USD", 900)
	e := billing.NewWebhookModuleEngine(pool, user, orgID)

	post := func(body string) {
		t.Helper()
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", body))
		if w.Code != http.StatusOK {
			t.Fatalf("webhook: want 200, got %d: %s", w.Code, w.Body)
		}
	}
	post(fmt.Sprintf(`{"type":"checkout.session.async_payment_failed","data":{"object":{"id":%q,"payment_status":"unpaid"}}}`, extID))
	post(fmt.Sprintf(`{"type":"checkout.session.expired","data":{"object":{"id":%q}}}`, extID))

	var subStatus, invStatus string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.subscriptions WHERE id = $1`, subID).Scan(&subStatus)
	pool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, invID).Scan(&invStatus)
	if subStatus != "past_due" {
		t.Errorf("subscription: want past_due after the decline, got %q", subStatus)
	}
	if invStatus != "pending" {
		t.Errorf("invoice: want pending, got %q", invStatus)
	}
	if got := linkStatuses(t, pool, invID); !slices.Equal(got, []string{"expired"}) {
		t.Errorf("link: want expired, got %v", got)
	}
	if n := countOrgOutboxRows(t, pool, events.RoutingKeyInvoiceFailed, orgID); n != 1 {
		t.Errorf("invoice failed events: want 1 (the decline only), got %d", n)
	}
}

// An add-on increase or extension left unpaid has an end: its checkout expiring
// leaves it payable, and the subscription's own end closes it. (A paid renewal
// voids both too, covered with the other stale-pricing cases.)
func TestCheckoutExpiry_AdditiveInvoice_StaysPayableUntilThePeriodEnds(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_checkout_expiry_additive"
		orgID = "00000000-0000-0000-0000-00000000c0e6"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "2 days")
	e := billing.NewWebhookModuleEngine(pool, user, orgID)

	invoices := map[string]string{}
	for _, kind := range []string{"extension", "addon_increase"} {
		var invID string
		if err := pool.QueryRow(t.Context(), `
			INSERT INTO billing.invoices (subscription_id, amount_cents, currency, kind)
			VALUES ($1, 900, 'USD', $2) RETURNING id`, subID, kind).Scan(&invID); err != nil {
			t.Fatalf("seed %s invoice: %v", kind, err)
		}
		extID := "cs_c0e6_" + kind
		seedPaymentLink(pool, invID, extID, "stripe", "USD", 900)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe",
			fmt.Sprintf(`{"type":"checkout.session.expired","data":{"object":{"id":%q}}}`, extID)))
		if w.Code != http.StatusOK {
			t.Fatalf("%s expiry webhook: want 200, got %d: %s", kind, w.Code, w.Body)
		}
		if got := invoiceStatus(t, pool, invID); got != "pending" {
			t.Errorf("%s after its checkout expired: want pending, got %q", kind, got)
		}
		invoices[kind] = invID
	}

	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET period_end = $1 WHERE id = $2`, past, subID); err != nil {
		t.Fatalf("end the period: %v", err)
	}
	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleSubscriptionCheck(t.Context(), encodeSubscriptionCheckEvent(subID, orgID, past)); err != nil {
		t.Fatalf("HandleSubscriptionCheck: %v", err)
	}
	for kind, invID := range invoices {
		if got := invoiceStatus(t, pool, invID); got != "failed" {
			t.Errorf("%s after the period ended: want failed, got %q", kind, got)
		}
	}
}
