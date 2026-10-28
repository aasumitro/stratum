package billing_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/platform/httpclient"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// orgStatusReader answers GetOrganizationByID with a fixed status or error.
// The embedded nil interface panics on any other method, which the paths
// under test never call.
type orgStatusReader struct {
	contracts.OrganizationReader
	status string
	err    error
}

func (r orgStatusReader) GetOrganizationByID(_ context.Context, id string) (*contracts.OrganizationInfo, error) {
	if r.err != nil {
		return nil, r.err
	}
	return &contracts.OrganizationInfo{ID: id, Status: r.status}, nil
}

// recordingTransport stands in for Stripe/Xendit: it records every request
// and answers each with status.
type recordingTransport struct {
	mu       sync.Mutex
	status   int
	requests []*http.Request
}

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.requests = append(rt.requests, req)
	rt.mu.Unlock()
	return &http.Response{
		StatusCode: rt.status, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader("{}")), Request: req,
	}, nil
}

// useProviderTransport routes the provider client through a recording
// transport for the rest of the test.
func useProviderTransport(t *testing.T, status int) *recordingTransport {
	t.Helper()
	rt := &recordingTransport{status: status}
	prev := httpclient.TrustedClient.Transport
	httpclient.TrustedClient.Transport = rt
	t.Cleanup(func() { httpclient.TrustedClient.Transport = prev })
	return rt
}

// seedLinkWithStatus seeds a payment link for invID in status.
func seedLinkWithStatus(t *testing.T, pool *pgxpool.Pool, invID, externalID, status string) {
	t.Helper()
	id := seedPaymentLink(pool, invID, externalID, "stripe", "USD", 900)
	if _, err := pool.Exec(t.Context(), `UPDATE billing.payment_links SET status = $2 WHERE id = $1`, id, status); err != nil {
		t.Fatalf("set link status: %v", err)
	}
}

// checkoutExpiries decodes the payment-link.expire outbox rows queued for
// orgID, sorted by external ID.
func checkoutExpiries(t *testing.T, pool *pgxpool.Pool, orgID string) []events.PaymentLinkExpire {
	t.Helper()
	var out []events.PaymentLinkExpire
	for _, payload := range queuedPayloads(t, pool, events.RoutingKeyPaymentLinkExpire, orgID) {
		evt, err := events.Decode[events.PaymentLinkExpire](payload)
		if err != nil {
			t.Fatalf("decode checkout expiry: %v", err)
		}
		out = append(out, evt)
	}
	slices.SortFunc(out, func(a, b events.PaymentLinkExpire) int { return strings.Compare(a.ExternalID, b.ExternalID) })
	return out
}

// linkStatuses returns the status of every payment link of invID.
func linkStatuses(t *testing.T, pool *pgxpool.Pool, invID string) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT status FROM billing.payment_links WHERE invoice_id = $1`, invID)
	if err != nil {
		t.Fatalf("read link statuses: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan link status: %v", err)
		}
		out = append(out, s)
	}
	return out
}

// provisionActive provisions orgID for user and forces its subscription
// active, returning the subscription ID.
func provisionActive(t *testing.T, pool *pgxpool.Pool, orgID, user, periodEnd string) string {
	t.Helper()
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	forceActive(t, pool, subID, periodEnd)
	return subID
}

func encodeEnvelope(t *testing.T, routingKey string, data any) []byte {
	t.Helper()
	body, err := json.Marshal(events.Envelope{
		ID: uuid.NewV7().String(), Type: routingKey, Source: "test", Time: time.Now(), Data: data,
	})
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	return body
}

// TestIntegration_Webhook_LatePayment_EndedSubscription_Reactivates: money
// for a period-backing invoice closed without payment — failed when the
// subscription ended, or voided when a cancel request or plan change
// superseded it — on a subscription that has since ended is applied like a
// Resume payment: active, a fresh period from now, the cancellation
// schedule cleared, and only billing's own suspension lifted.
func TestIntegration_Webhook_LatePayment_EndedSubscription_Reactivates(t *testing.T) {
	cases := []struct{ name, orgID, extID, subStatus, kind, invStatus string }{
		{"activation failed at expiry", "00000000-0000-0000-0000-000000000a74", "stripe_sess_a74", "expired", "activation", "failed"},
		{"activation failed when the cancel took effect", "00000000-0000-0000-0000-000000000a75", "stripe_sess_a75", "cancelled", "activation", "failed"},
		{"renewal voided by the cancel request", "00000000-0000-0000-0000-000000000a76", "stripe_sess_a76", "cancelled", "subscription", "void"},
		{"renewal voided before the subscription expired", "00000000-0000-0000-0000-000000000a77", "stripe_sess_a77", "expired", "subscription", "void"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testPoolBilling(t)
			user := "integ_billing_wh_late_ended_" + tc.orgID[len(tc.orgID)-3:]
			setupBillingTest(t, pool, tc.orgID)
			subID, invID := seedClosedInvoiceState(t, pool, tc.orgID, user, tc.extID, tc.subStatus, tc.kind, tc.invStatus)
			if _, err := pool.Exec(t.Context(),
				`UPDATE billing.subscriptions SET scheduled_cancel_at = now() WHERE id = $1`, subID); err != nil {
				t.Fatalf("seed a leftover cancellation schedule: %v", err)
			}

			suspender := &stubOrgSuspender{}
			e := billing.NewWebhookModuleEngineWithOrgReader(pool, user, tc.orgID, suspender, orgStatusReader{status: "suspended"})
			postPaidStripeWebhook(t, e, tc.extID)

			var status, cycle string
			var periodEnd time.Time
			var scheduledCancelAt *time.Time
			if err := pool.QueryRow(t.Context(),
				`SELECT status, cycle, period_end, scheduled_cancel_at FROM billing.subscriptions WHERE id = $1`, subID,
			).Scan(&status, &cycle, &periodEnd, &scheduledCancelAt); err != nil {
				t.Fatalf("read subscription: %v", err)
			}
			want := time.Now().AddDate(0, 1, 0)
			if cycle == "yearly" {
				want = time.Now().AddDate(1, 0, 0)
			}
			if status != "active" || scheduledCancelAt != nil || periodEnd.Sub(want).Abs() > time.Minute {
				t.Errorf("subscription: want active, no cancellation schedule, period_end ~%s; got %q, %v, %s",
					want, status, scheduledCancelAt, periodEnd)
			}
			var invStatus string
			var payments int
			pool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, invID).Scan(&invStatus)
			pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.payments WHERE invoice_id = $1`, invID).Scan(&payments)
			if invStatus != "paid" || payments != 1 {
				t.Errorf("invoice: want paid with one payment row, got %q with %d", invStatus, payments)
			}
			wantReasons := []string{"subscription expired", "subscription cancelled"}
			if !slices.Equal(suspender.unsuspended, []string{tc.orgID}) || len(suspender.unsuspendReasons) != 1 ||
				!slices.Equal(suspender.unsuspendReasons[0], wantReasons) {
				t.Errorf("unsuspend: want one call for %s limited to %v, got %v %v",
					tc.orgID, wantReasons, suspender.unsuspended, suspender.unsuspendReasons)
			}
			for _, key := range []string{events.RoutingKeyInvoicePaid, events.RoutingKeySubscriptionResumed} {
				if n := countOrgOutboxRows(t, pool, key, tc.orgID); n != 1 {
					t.Errorf("outbox %s: want 1, got %d", key, n)
				}
			}
		})
	}
}

// TestIntegration_CancelSubscription_ClosesRenewalCheckoutsAtProvider: the
// renewal a cancel voids has its open links — pending, or failed and still
// retryable — expired and queued for closing at the provider; a link
// already expired is left alone.
func TestIntegration_CancelSubscription_ClosesRenewalCheckoutsAtProvider(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_expiry_cancel_user"
		orgID = "00000000-0000-0000-0000-000000000e01"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "10 days")
	invID := seedInvoice(pool, subID, "USD", 900)
	seedLinkWithStatus(t, pool, invID, "cs_e01_a_open", "pending")
	seedLinkWithStatus(t, pool, invID, "cs_e01_b_failed", "failed")
	seedLinkWithStatus(t, pool, invID, "cs_e01_c_old", "expired")

	e := billing.NewModuleEngineWithRealRLS(pool, user, orgID, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel", `{"reason":"too_expensive"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("cancel: want 200, got %d: %s", w.Code, w.Body)
	}

	want := []events.PaymentLinkExpire{
		{Provider: "stripe", ExternalID: "cs_e01_a_open"},
		{Provider: "stripe", ExternalID: "cs_e01_b_failed"},
	}
	if got := checkoutExpiries(t, pool, orgID); !slices.Equal(got, want) {
		t.Errorf("checkout expiries: want %v, got %v", want, got)
	}
	for _, s := range linkStatuses(t, pool, invID) {
		if s != "expired" {
			t.Errorf("every link of the voided renewal: want expired, got %q", s)
		}
	}
}

// TestIntegration_ChangePlan_ClosesVoidedCheckoutsAtProvider: a plan change
// voids every pending invoice and queues closing their checkouts.
func TestIntegration_ChangePlan_ClosesVoidedCheckoutsAtProvider(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_expiry_plan_user"
		orgID = "00000000-0000-0000-0000-000000000e02"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "20 days")
	var invID string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO billing.invoices (subscription_id, amount_cents, currency, kind)
		VALUES ($1, 900, 'USD', 'extension') RETURNING id`, subID).Scan(&invID); err != nil {
		t.Fatalf("seed extension invoice: %v", err)
	}
	seedLinkWithStatus(t, pool, invID, "cs_e02_ext", "pending")

	upgradeToGrowth(t, pool, user, orgID)

	want := []events.PaymentLinkExpire{{Provider: "stripe", ExternalID: "cs_e02_ext"}}
	if got := checkoutExpiries(t, pool, orgID); !slices.Equal(got, want) {
		t.Errorf("checkout expiries: want %v, got %v", want, got)
	}
}

// TestIntegration_ExpireIfDue_ClosesFailedCheckoutsAtProvider: the end of a
// subscription fails its pending invoices and queues closing their
// checkouts in the same transaction.
func TestIntegration_ExpireIfDue_ClosesFailedCheckoutsAtProvider(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_expiry_end_user"
		orgID = "00000000-0000-0000-0000-000000000e03"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "2 days")
	invID := seedInvoice(pool, subID, "USD", 900)
	seedLinkWithStatus(t, pool, invID, "cs_e03_renewal", "pending")
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET period_end = now() - interval '1 minute' WHERE id = $1`, subID); err != nil {
		t.Fatalf("end the period: %v", err)
	}
	end, _ := getSubscriptionExpectedEnd(pool, subID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleSubscriptionCheck(t.Context(), encodeSubscriptionCheckEvent(subID, orgID, end)); err != nil {
		t.Fatalf("expiry check: %v", err)
	}

	want := []events.PaymentLinkExpire{{Provider: "stripe", ExternalID: "cs_e03_renewal"}}
	if got := checkoutExpiries(t, pool, orgID); !slices.Equal(got, want) {
		t.Errorf("checkout expiries: want %v, got %v", want, got)
	}
}

// TestIntegration_OrganizationDeleted_ClosesCheckoutsAtProvider: deleting
// the organization fails its open invoices and queues closing their
// checkouts.
func TestIntegration_OrganizationDeleted_ClosesCheckoutsAtProvider(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_expiry_deleted_user"
		orgID = "00000000-0000-0000-0000-000000000e04"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "10 days")
	invID := seedInvoice(pool, subID, "USD", 900)
	seedLinkWithStatus(t, pool, invID, "cs_e04_open", "pending")

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	body := encodeEnvelope(t, events.RoutingKeyOrganizationDeleted,
		events.OrganizationDeleted{OrganizationID: orgID, DeletedAt: time.Now()})
	if err := mod.Worker.HandleOrganizationDeleted(t.Context(), body); err != nil {
		t.Fatalf("organization deleted: %v", err)
	}

	want := []events.PaymentLinkExpire{{Provider: "stripe", ExternalID: "cs_e04_open"}}
	if got := checkoutExpiries(t, pool, orgID); !slices.Equal(got, want) {
		t.Errorf("checkout expiries: want %v, got %v", want, got)
	}
}

// TestIntegration_CreatePaymentLink_ClosesPreviousCheckoutAtProvider: asking
// for a new link expires the invoice's open ones — a failed one included,
// since its hosted page still takes a retry — and queues closing them, even
// when the new checkout then fails to be created.
func TestIntegration_CreatePaymentLink_ClosesPreviousCheckoutAtProvider(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_expiry_regen_user"
		orgID = "00000000-0000-0000-0000-000000000e05"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "10 days")
	invID := seedInvoice(pool, subID, "USD", 900)
	seedLinkWithStatus(t, pool, invID, "cs_e05_failed", "failed")
	useProviderTransport(t, http.StatusInternalServerError)

	e := billing.NewWebhookModuleEngineWithSuspender(pool, user, orgID, &stubOrgSuspender{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/invoices/"+invID+"/pay", ""))
	if w.Code == http.StatusOK || w.Code == http.StatusCreated {
		t.Fatalf("pay: want the provider failure to surface, got %d", w.Code)
	}

	want := []events.PaymentLinkExpire{{Provider: "stripe", ExternalID: "cs_e05_failed"}}
	if got := checkoutExpiries(t, pool, orgID); !slices.Equal(got, want) {
		t.Errorf("checkout expiries: want %v, got %v", want, got)
	}
	if got := linkStatuses(t, pool, invID); !slices.Equal(got, []string{"expired"}) {
		t.Errorf("previous link: want expired, got %v", got)
	}
}

// TestIntegration_Webhook_Paid_ClosesTheInvoicesOtherCheckouts: once one
// checkout pays an invoice, any other still-open checkout for it is expired
// and queued for closing at the provider, so it can't take a second
// payment; the paying link stays paid.
func TestIntegration_Webhook_Paid_ClosesTheInvoicesOtherCheckouts(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_expiry_paid_user"
		orgID = "00000000-0000-0000-0000-000000000e09"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "2 days")
	invID := seedInvoice(pool, subID, "USD", 900)
	seedLinkWithStatus(t, pool, invID, "cs_e09_paying", "pending")
	seedLinkWithStatus(t, pool, invID, "cs_e09_other", "pending")

	postPaidStripeWebhook(t, billing.NewWebhookModuleEngineWithSuspender(pool, user, orgID, &stubOrgSuspender{}), "cs_e09_paying")

	want := []events.PaymentLinkExpire{{Provider: "stripe", ExternalID: "cs_e09_other"}}
	if got := checkoutExpiries(t, pool, orgID); !slices.Equal(got, want) {
		t.Errorf("checkout expiries: want %v, got %v", want, got)
	}
	var paying, other string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.payment_links WHERE external_id = 'cs_e09_paying'`).Scan(&paying)
	pool.QueryRow(t.Context(), `SELECT status FROM billing.payment_links WHERE external_id = 'cs_e09_other'`).Scan(&other)
	if paying != "paid" || other != "expired" {
		t.Errorf("links: want paying=paid other=expired, got %q %q", paying, other)
	}
}

// TestHandlePaymentLinkExpire_CallsTheProvider covers the consumer that
// closes a checkout at its provider: the request each provider gets, which
// answers end the attempt (closed, or no longer open) and which are
// redelivered, and the calls it never makes.
func TestHandlePaymentLinkExpire_CallsTheProvider(t *testing.T) {
	const stripeKey, xenditKey = "sk_test_expire", "xnd_development_expire"
	cfg := billing.ProviderConfig{StripeAPIKey: stripeKey, XenditAPIKey: xenditKey}
	cases := []struct {
		name               string
		cfg                billing.ProviderConfig
		provider, external string
		status             int
		wantURL, wantKey   string
		wantErr            bool
	}{
		{"stripe session closed", cfg, "stripe", "cs_1", 200, "https://api.stripe.com/v1/checkout/sessions/cs_1/expire", stripeKey, false},
		{"xendit invoice closed", cfg, "xendit", "inv_1", 200, "https://api.xendit.co/invoices/inv_1/expire!", xenditKey, false},
		{"stripe session no longer open", cfg, "stripe", "cs_2", 400, "https://api.stripe.com/v1/checkout/sessions/cs_2/expire", stripeKey, false},
		{"xendit invoice unknown", cfg, "xendit", "inv_2", 404, "https://api.xendit.co/invoices/inv_2/expire!", xenditKey, false},
		{"stripe rate limited", cfg, "stripe", "cs_3", 429, "https://api.stripe.com/v1/checkout/sessions/cs_3/expire", stripeKey, true},
		{"xendit server error", cfg, "xendit", "inv_3", 503, "https://api.xendit.co/invoices/inv_3/expire!", xenditKey, true},
		{"stripe not configured", billing.ProviderConfig{}, "stripe", "cs_4", 200, "", "", false},
		{"unknown provider", cfg, "paypal", "pp_1", 200, "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := useProviderTransport(t, tc.status)
			mod := billing.New(nil, tc.cfg, nil, nil, nil)
			body := encodeEnvelope(t, events.RoutingKeyPaymentLinkExpire,
				events.PaymentLinkExpire{Provider: tc.provider, ExternalID: tc.external})

			err := mod.Worker.HandlePaymentLinkExpire(t.Context(), body)
			if (err != nil) != tc.wantErr {
				t.Fatalf("want error=%v, got %v", tc.wantErr, err)
			}
			if tc.wantURL == "" {
				if len(rt.requests) != 0 {
					t.Fatalf("want no provider call, got %d", len(rt.requests))
				}
				return
			}
			if len(rt.requests) != 1 {
				t.Fatalf("want exactly one provider call, got %d", len(rt.requests))
			}
			req := rt.requests[0]
			user, _, _ := req.BasicAuth()
			if req.Method != http.MethodPost || req.URL.String() != tc.wantURL || user != tc.wantKey {
				t.Errorf("provider call: want POST %s as %s, got %s %s as %s",
					tc.wantURL, tc.wantKey, req.Method, req.URL, user)
			}
		})
	}
}

// TestIntegration_HandleInvoiceMarkPaid_AppliesLikeAPayment: an operator's
// mark-paid from Studio has a confirmed payment's effects — a renewal rolls
// the period on and schedules the next renewal; a Resume invoice
// reactivates and lifts billing's suspension — without a payment row, and
// an invoice that is no longer pending (or doesn't exist) is left alone.
func TestIntegration_HandleInvoiceMarkPaid_AppliesLikeAPayment(t *testing.T) {
	t.Run("renewal on an active subscription", func(t *testing.T) {
		pool := testPoolBilling(t)
		const (
			user  = "integ_billing_markpaid_renew_user"
			orgID = "00000000-0000-0000-0000-000000000e06"
		)
		setupBillingTest(t, pool, orgID)
		subID := provisionActive(t, pool, orgID, user, "2 days")
		oldEnd, _ := getSubscriptionExpectedEnd(pool, subID)
		invID := seedInvoice(pool, subID, "USD", 900)
		seedLinkWithStatus(t, pool, invID, "cs_e06_customer", "pending")

		mod := billing.New(pool, billing.ProviderConfig{}, stubRefReader{}, &stubOrgSuspender{}, pool)
		if err := mod.Worker.HandleInvoiceMarkPaid(t.Context(),
			encodeEnvelope(t, events.RoutingKeyInvoiceMarkPaid, events.InvoiceMarkPaid{InvoiceID: invID})); err != nil {
			t.Fatalf("mark paid: %v", err)
		}
		// The customer's own checkout must not take a second payment.
		want := []events.PaymentLinkExpire{{Provider: "stripe", ExternalID: "cs_e06_customer"}}
		if got := checkoutExpiries(t, pool, orgID); !slices.Equal(got, want) {
			t.Errorf("checkout expiries: want %v, got %v", want, got)
		}
		if got := linkStatuses(t, pool, invID); !slices.Equal(got, []string{"expired"}) {
			t.Errorf("customer's link: want expired, got %v", got)
		}

		var invStatus string
		var payments int
		pool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, invID).Scan(&invStatus)
		pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.payments WHERE invoice_id = $1`, invID).Scan(&payments)
		if invStatus != "paid" || payments != 0 {
			t.Errorf("invoice: want paid with no payment row, got %q with %d", invStatus, payments)
		}
		newEnd, _ := getSubscriptionExpectedEnd(pool, subID)
		if want := oldEnd.AddDate(0, 1, 0); !newEnd.Equal(want) {
			t.Errorf("period: want rolled to %s, got %s", want, newEnd)
		}
		var actor string
		pool.QueryRow(t.Context(),
			`SELECT changed_by FROM billing.subscription_history WHERE subscription_id = $1 AND action = 'renew'`, subID,
		).Scan(&actor)
		if actor != "operator" {
			t.Errorf("renew history: want changed_by=operator for a Studio mark-paid, got %q", actor)
		}
		paid := queuedPayloads(t, pool, events.RoutingKeyInvoicePaid, orgID)
		if len(paid) != 1 {
			t.Fatalf("outbox %s: want 1, got %d", events.RoutingKeyInvoicePaid, len(paid))
		}
		if evt, _ := events.Decode[events.InvoicePaid](paid[0]); evt.AmountCents != 900 || evt.Currency != "USD" {
			t.Errorf("InvoicePaid: want the invoice's 900 USD, got %d %s", evt.AmountCents, evt.Currency)
		}
		checks := queuedPayloads(t, pool, events.RoutingKeySubscriptionCheck, orgID)
		if len(checks) == 0 || !queuedCheckEnd(t, checks[len(checks)-1]).Equal(newEnd) {
			t.Errorf("want the next expiry check queued for the new end %s", newEnd)
		}
	})

	t.Run("resume invoice on a cancelled subscription", func(t *testing.T) {
		pool := testPoolBilling(t)
		const (
			user  = "integ_billing_markpaid_resume_user"
			orgID = "00000000-0000-0000-0000-000000000e07"
		)
		setupBillingTest(t, pool, orgID)
		subID := provisionActive(t, pool, orgID, user, "1 day")
		if _, err := pool.Exec(t.Context(), `
			UPDATE billing.subscriptions SET status = 'cancelled', period_end = now() - interval '1 day'
			WHERE id = $1`, subID); err != nil {
			t.Fatalf("end the subscription: %v", err)
		}
		invID := seedInvoice(pool, subID, "USD", 900)

		suspender := &stubOrgSuspender{}
		mod := billing.New(pool, billing.ProviderConfig{}, stubRefReader{}, suspender, pool)
		if err := mod.Worker.HandleInvoiceMarkPaid(t.Context(),
			encodeEnvelope(t, events.RoutingKeyInvoiceMarkPaid, events.InvoiceMarkPaid{InvoiceID: invID})); err != nil {
			t.Fatalf("mark paid: %v", err)
		}

		var status string
		var periodEnd time.Time
		pool.QueryRow(t.Context(), `SELECT status, period_end FROM billing.subscriptions WHERE id = $1`, subID).Scan(&status, &periodEnd)
		if want := time.Now().AddDate(0, 1, 0); status != "active" || periodEnd.Sub(want).Abs() > time.Minute {
			t.Errorf("subscription: want active with a fresh period ~%s, got %q %s", want, status, periodEnd)
		}
		if !slices.Equal(suspender.unsuspended, []string{orgID}) || len(suspender.unsuspendReasons) != 1 ||
			!slices.Equal(suspender.unsuspendReasons[0], []string{"subscription expired", "subscription cancelled"}) {
			t.Errorf("unsuspend: want billing's reasons lifted for %s, got %v %v", orgID, suspender.unsuspended, suspender.unsuspendReasons)
		}
		if n := countOrgOutboxRows(t, pool, events.RoutingKeySubscriptionResumed, orgID); n != 1 {
			t.Errorf("outbox %s: want 1, got %d", events.RoutingKeySubscriptionResumed, n)
		}
	})

	t.Run("invoice no longer pending, or unknown", func(t *testing.T) {
		pool := testPoolBilling(t)
		const (
			user  = "integ_billing_markpaid_noop_user"
			orgID = "00000000-0000-0000-0000-000000000e08"
		)
		setupBillingTest(t, pool, orgID)
		subID := provisionActive(t, pool, orgID, user, "2 days")
		oldEnd, _ := getSubscriptionExpectedEnd(pool, subID)
		invID := seedInvoice(pool, subID, "USD", 900)
		if _, err := pool.Exec(t.Context(), `UPDATE billing.invoices SET status = 'void' WHERE id = $1`, invID); err != nil {
			t.Fatalf("void the invoice: %v", err)
		}

		mod := billing.New(pool, billing.ProviderConfig{}, stubRefReader{}, &stubOrgSuspender{}, pool)
		for _, id := range []string{invID, uuid.NewV7().String()} {
			if err := mod.Worker.HandleInvoiceMarkPaid(t.Context(),
				encodeEnvelope(t, events.RoutingKeyInvoiceMarkPaid, events.InvoiceMarkPaid{InvoiceID: id})); err != nil {
				t.Fatalf("mark paid %s: want acknowledged, got %v", id, err)
			}
		}
		var invStatus string
		pool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, invID).Scan(&invStatus)
		newEnd, _ := getSubscriptionExpectedEnd(pool, subID)
		if invStatus != "void" || !newEnd.Equal(oldEnd) {
			t.Errorf("want nothing applied, got invoice %q and period %s -> %s", invStatus, oldEnd, newEnd)
		}
		if n := countOrgOutboxRows(t, pool, events.RoutingKeyInvoicePaid, orgID); n != 0 {
			t.Errorf("outbox %s: want 0, got %d", events.RoutingKeyInvoicePaid, n)
		}
	})
}
