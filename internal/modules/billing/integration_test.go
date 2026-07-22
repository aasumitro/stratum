package billing_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// stubRefReader satisfies contracts.CountryTaxReader — the only outside-schema
// concern billing consumes through an injected reader. Catalog data is
// billing's own schema, read via its own repo, so tests exercise real seed
// prices (db/migrations/000004_billing.up.sql) rather than a fixture.
type stubRefReader struct{}

func (stubRefReader) GetCountryTaxRate(_ context.Context, _ string) (int, error) { return 0, nil }

// encodeOrganizationCreatedEventFor builds an event body for a specific
// createdBy subject, with plan="solo"/cycle="monthly" — plan and cycle are
// required at the API boundary now, so no test exercising ordinary
// provisioning should send an empty one.
func encodeOrganizationCreatedEventFor(organizationID, createdBy string) []byte {
	return encodeOrganizationCreatedEventWithPlan(organizationID, createdBy, "solo", "monthly")
}

// encodeOrganizationCreatedEventWithPlan builds an OrganizationCreated event
// body carrying an explicit Plan and Cycle — used to verify
// HandleOrganizationCreated provisions the creator's chosen plan instead of
// hardcoding "solo".
func encodeOrganizationCreatedEventWithPlan(organizationID, createdBy, plan, cycle string) []byte {
	env := events.Envelope{
		ID: "evt-" + organizationID, Type: events.RoutingKeyOrganizationCreated,
		Source: "organization", Time: time.Now(), OrgID: organizationID,
		Data: events.OrganizationCreated{
			OrganizationID: organizationID, Slug: "ws-" + organizationID[len(organizationID)-4:],
			Name: "Test WS", CreatedBy: createdBy, Plan: plan, Cycle: cycle, CreatedAt: time.Now(),
		},
	}
	b, _ := json.Marshal(env)
	return b
}

// encodeOrganizationCreatedEventWithCart builds an OrganizationCreated event
// carrying addons/coupon chosen at organization-creation time — verifies
// provisionSubscription attaches both before composing the subscription's
// first invoice.
func encodeOrganizationCreatedEventWithCart(
	organizationID, createdBy, plan, cycle string, addons []events.AddonSelection, couponCode string,
) []byte {
	env := events.Envelope{
		ID: "evt-" + organizationID, Type: events.RoutingKeyOrganizationCreated,
		Source: "organization", Time: time.Now(), OrgID: organizationID,
		Data: events.OrganizationCreated{
			OrganizationID: organizationID, Slug: "ws-" + organizationID[len(organizationID)-4:],
			Name: "Test WS", CreatedBy: createdBy, Plan: plan, Cycle: cycle,
			Addons: addons, CouponCode: couponCode, CreatedAt: time.Now(),
		},
	}
	b, _ := json.Marshal(env)
	return b
}

// encodeSubscriptionCheckEvent builds a SubscriptionCheck event body.
func encodeSubscriptionCheckEvent(subID, subjectID string, expectedEnd time.Time) []byte {
	env := events.Envelope{
		ID: "check-" + subID, Type: events.RoutingKeySubscriptionCheck,
		Source: "billing", Time: time.Now(), OrgID: subjectID,
		Data: events.SubscriptionCheck{
			SubscriptionID: subID, SubjectType: "organization",
			SubjectID: subjectID, ExpectedEnd: expectedEnd,
		},
	}
	b, _ := json.Marshal(env)
	return b
}

// billingURL returns the base billing API path for an organization.
func billingURL(orgID string) string { return "/api/organizations/" + orgID + "/billing" }

// getSubscriptionData provisions, calls GET /billing, and returns the data map.
func getSubscriptionData(t *testing.T, e interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, orgID string) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(orgID), ""))
	if w.Code != http.StatusOK {
		t.Fatalf("GET billing: want 200, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	return resp["data"].(map[string]any)
}

// --- Trial logic ---

func TestIntegration_FirstOrganization_GetsTrial(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_trial_user"
		orgID = "00000000-0000-0000-0000-000000000d01"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	mod.SetOrganizationReader(organization.New(pool, messaging.NoopPublisher{}))
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	data := getSubscriptionData(t, billing.NewModuleEngine(pool, user, orgID), orgID)
	if data["status"] != "trialing" {
		t.Errorf("first organization: want status=trialing, got %v", data["status"])
	}
}

func TestIntegration_SecondOrganization_NoTrial(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_notrial_user"
		wsID1 = "00000000-0000-0000-0000-000000000d02"
		wsID2 = "00000000-0000-0000-0000-000000000d03"
	)
	cleanupBillingByOrganization(pool, wsID1)
	cleanupBillingByOrganization(pool, wsID2)
	t.Cleanup(func() {
		cleanupBillingByOrganization(pool, wsID1)
		cleanupBillingByOrganization(pool, wsID2)
	})

	// seed organization.organizations so ListOwnedOrganizationIDs resolves the owner
	seedBillingOrganization(pool, wsID1, user)
	seedBillingOrganization(pool, wsID2, user)

	mod := billing.NewModuleForTest(pool, nil)
	mod.SetOrganizationReader(organization.New(pool, messaging.NoopPublisher{}))
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID2, user)); err != nil {
		t.Fatalf("second provision: %v", err)
	}

	data := getSubscriptionData(t, billing.NewModuleEngine(pool, user, wsID2), wsID2)
	if data["status"] != "active" {
		t.Errorf("second organization: want status=active (no trial), got %v", data["status"])
	}
}

// TestIntegration_SecondOrganization_WithCart_AddonsAndCouponOnFirstInvoice
// verifies the "create org" cart end-to-end at the point it actually
// matters: a non-trial (2nd+) organization's addons and coupon, carried on
// OrganizationCreated, land on the very first invoice — composed
// synchronously inside the same provisionSubscription call, with no window
// for a separate attach-addon/redeem-coupon call to race into.
func TestIntegration_SecondOrganization_WithCart_AddonsAndCouponOnFirstInvoice(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user       = "integ_billing_cart_user"
		wsID1      = "00000000-0000-0000-0000-000000000d04"
		wsID2      = "00000000-0000-0000-0000-000000000d05"
		couponCode = "CARTCOUPON_D05"
	)
	cleanupBillingByOrganization(pool, wsID1)
	cleanupBillingByOrganization(pool, wsID2)
	t.Cleanup(func() {
		cleanupBillingByOrganization(pool, wsID1)
		cleanupBillingByOrganization(pool, wsID2)
	})
	seedBillingOrganization(pool, wsID1, user)
	seedBillingOrganization(pool, wsID2, user)
	seedCoupon(t, pool, couponCode, couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(100), Cadence: "once"})

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(organization.New(pool, messaging.NoopPublisher{}))
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	cart := []events.AddonSelection{{AddonID: "extra-seat", Quantity: 2}}
	if err := mod.Worker.HandleOrganizationCreated(t.Context(),
		encodeOrganizationCreatedEventWithCart(wsID2, user, "solo", "monthly", cart, couponCode)); err != nil {
		t.Fatalf("second provision with cart: %v", err)
	}

	subID := getSubscriptionID(pool, wsID2)

	var addonQty int
	if err := pool.QueryRow(t.Context(),
		`SELECT quantity FROM billing.subscription_addons WHERE subscription_id = $1 AND addon_id = 'extra-seat'`, subID,
	).Scan(&addonQty); err != nil || addonQty != 2 {
		t.Errorf("want extra-seat quantity=2 attached, got %d (err=%v)", addonQty, err)
	}

	var redemptionCount int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.coupon_redemptions WHERE coupon_id = $1 AND subscription_id = $2`, couponCode, subID,
	).Scan(&redemptionCount)
	if redemptionCount != 1 {
		t.Errorf("want exactly 1 coupon redemption recorded, got %d", redemptionCount)
	}

	// solo plan $9.00/mo (900c, seed data) + extra-seat x2 @ $1.00/mo (200c) - $1.00 coupon (100c) = 1000c.
	var amountCents int64
	pool.QueryRow(t.Context(),
		`SELECT amount_cents FROM billing.invoices WHERE subscription_id = $1 ORDER BY created_at DESC LIMIT 1`, subID,
	).Scan(&amountCents)
	if amountCents != 1000 {
		t.Errorf("first invoice: want 1000 (900 plan + 200 addon - 100 coupon), got %d", amountCents)
	}
}

// --- Cancel ---

func TestIntegration_CancelSubscription_Active(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_cancel_user"
		orgID = "00000000-0000-0000-0000-000000000d04"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("cancel: want 200, got %d: %s", w.Code, w.Body)
	}

	data := getSubscriptionData(t, e, orgID)
	if data["status"] != "cancelled" {
		t.Errorf("after cancel: want status=cancelled, got %v", data["status"])
	}
}

func TestIntegration_CancelSubscription_AlreadyCancelled(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_dblcancel_user"
		orgID = "00000000-0000-0000-0000-000000000d05"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID)
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel", ""))

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel", ""))
	if w.Code == http.StatusOK {
		t.Errorf("double cancel should fail, got 200")
	}
}

// --- Resume ---

// TestIntegration_ResumeSubscription_Cancelled covers cancel->resume for a
// *trialing* subscription (provisionSubscription always trials a user's
// first-ever organization) — must resume back into "trialing", not "active",
// since the trial window is still open and no invoice was ever created.
// Previously this test asserted status=="active" here, which was actually
// asserting the bug: the trial banner disappearing after cancel+resume
// because resumeSubscription unconditionally forced status to "active".
func TestIntegration_ResumeSubscription_Cancelled(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_resume_user"
		orgID = "00000000-0000-0000-0000-000000000d06"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID)
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel", ""))

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/resume", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("resume cancelled: want 200, got %d: %s", w.Code, w.Body)
	}

	data := getSubscriptionData(t, e, orgID)
	if data["status"] != "trialing" {
		t.Errorf("after resume within trial window: want status=trialing, got %v", data["status"])
	}
	if data["trial_end"] == nil || data["trial_end"] == "" {
		t.Error("after resume within trial window: want trial_end still set")
	}
}

// TestIntegration_ResumeSubscription_CancelledAfterTrialExpired covers the
// other branch: a subscription cancelled once its trial window has already
// passed resumes into "active" (the pre-existing behavior, unchanged).
func TestIntegration_ResumeSubscription_CancelledAfterTrialExpired(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_resume_expiredtrial_user"
		orgID = "00000000-0000-0000-0000-000000000d12"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)

	// Force the trial window into the past, as if it had genuinely expired
	// before the subscription was cancelled.
	pool.Exec(t.Context(), `UPDATE billing.subscriptions SET trial_end = now() - interval '1 day' WHERE id = $1`, subID)

	e := billing.NewModuleEngine(pool, user, orgID)
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel", ""))

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/resume", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("resume cancelled: want 200, got %d: %s", w.Code, w.Body)
	}

	data := getSubscriptionData(t, e, orgID)
	if data["status"] != "active" {
		t.Errorf("after resume with expired trial: want status=active, got %v", data["status"])
	}
}

func TestIntegration_ResumeSubscription_ActiveFails(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_resumeact_user"
		orgID = "00000000-0000-0000-0000-000000000d07"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	// second organization → active (not trialing)
	e := billing.NewModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/resume", ""))
	// A business-rule rejection (wrong status), not an internal error — must
	// stay 422, not 500, even after resumeSubscription started distinguishing
	// genuine internal failures from ErrSubscriptionNotResumable.
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("resuming an active subscription: want 422, got %d: %s", w.Code, w.Body)
	}
}

// --- Change plan ---

func TestIntegration_ChangePlan_UpgradeSoloToGrowth(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_upgrade_user"
		orgID = "00000000-0000-0000-0000-000000000d08"
	)
	setupBillingTest(t, pool, orgID)

	// second organization (same user already has d02-d03 subscriptions, so no trial here)
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, billingURL(orgID)+"/plan", `{"plan":"growth","cycle":"monthly"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("changePlan: want 200, got %d: %s", w.Code, w.Body)
	}

	data := getSubscriptionData(t, e, orgID)
	if data["plan"] != "growth" {
		t.Errorf("after upgrade: want plan=growth, got %v", data["plan"])
	}
}

func TestIntegration_ChangePlan_UnknownPlan(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_badplan_user"
		orgID = "00000000-0000-0000-0000-000000000d09"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})
	w := httptest.NewRecorder()
	// "enterprise" isn't in stubRefReader's plan map (not a catalog-plan
	// validation shortcut anymore — changePlanRequest.Plan has no oneof —
	// this now exercises changePlan's own refReader.GetPlanByID lookup,
	// which is the real, dynamic catalog check).
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, billingURL(orgID)+"/plan", `{"plan":"enterprise","cycle":"monthly"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown plan should return 422, got %d: %s", w.Code, w.Body)
	}
}

// --- Expire ---

func TestIntegration_ExpireIfDue_ActivePastEnd(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_expire_user"
		orgID = "00000000-0000-0000-0000-000000000d10"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	// Force period_end into the past so expireIfDue triggers.
	past := time.Now().Add(-time.Second)
	_, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET period_end = $1, trial_end = NULL WHERE subject_type = 'organization' AND subject_id = $2`,
		past, orgID)
	if err != nil {
		t.Fatalf("set past period_end: %v", err)
	}

	// Look up subscription ID for the check event.
	var subID string
	pool.QueryRow(t.Context(),
		`SELECT id FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1`, orgID,
	).Scan(&subID)

	if err := mod.Worker.HandleSubscriptionCheck(t.Context(), encodeSubscriptionCheckEvent(subID, orgID, past)); err != nil {
		t.Fatalf("HandleSubscriptionCheck: %v", err)
	}

	data := getSubscriptionData(t, billing.NewModuleEngine(pool, user, orgID), orgID)
	if data["status"] != "expired" {
		t.Errorf("after expiry check: want status=expired, got %v", data["status"])
	}
}

func TestIntegration_ExpireIfDue_FutureEnd_NoChange(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_noexpire_user"
		orgID = "00000000-0000-0000-0000-000000000d11"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	var subID string
	pool.QueryRow(t.Context(),
		`SELECT id FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1`, orgID,
	).Scan(&subID)

	future := time.Now().Add(30 * 24 * time.Hour)
	if err := mod.Worker.HandleSubscriptionCheck(t.Context(), encodeSubscriptionCheckEvent(subID, orgID, future)); err != nil {
		t.Fatalf("HandleSubscriptionCheck: %v", err)
	}

	data := getSubscriptionData(t, billing.NewModuleEngine(pool, user, orgID), orgID)
	status := data["status"].(string)
	if status == "expired" {
		t.Errorf("subscription with future end should not expire, got status=expired")
	}
}

// --- Webhook processing ---

func TestIntegration_StripeWebhook_MarksInvoicePaid(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_wh_stripe_user"
		orgID = "00000000-0000-0000-0000-000000000f01"
		extID = "stripe_sess_f01"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	subID := getSubscriptionID(pool, orgID)
	invID := seedInvoice(pool, subID, "USD", 900)
	seedPaymentLink(pool, invID, extID, "stripe", "USD", 900)

	e := billing.NewWebhookModuleEngine(pool, user, orgID)
	payload := `{"type":"checkout.session.completed","data":{"object":{"id":"` + extID + `","payment_status":"paid"}}}`
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if w.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", w.Code, w.Body)
	}

	var invStatus string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, invID).Scan(&invStatus)
	if invStatus != "paid" {
		t.Errorf("invoice: want status=paid, got %q", invStatus)
	}

	var paymentCount int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.payments WHERE invoice_id = $1`, invID).Scan(&paymentCount)
	if paymentCount != 1 {
		t.Errorf("want 1 payment record, got %d", paymentCount)
	}
}

func TestIntegration_XenditWebhook_MarksInvoicePaid(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_wh_xendit_user"
		orgID = "00000000-0000-0000-0000-000000000f02"
		extID = "xendit_inv_f02"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	subID := getSubscriptionID(pool, orgID)
	invID := seedInvoice(pool, subID, "IDR", 13500)
	seedPaymentLink(pool, invID, extID, "xendit", "IDR", 13500)

	e := billing.NewWebhookModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/xendit",
		`{"id":"`+extID+`","status":"PAID"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", w.Code, w.Body)
	}

	var invStatus string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, invID).Scan(&invStatus)
	if invStatus != "paid" {
		t.Errorf("invoice: want status=paid, got %q", invStatus)
	}
}

func TestIntegration_Webhook_UnknownExternalID_ACKs(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_wh_unknown_user"
		orgID = "00000000-0000-0000-0000-000000000f03"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewWebhookModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe",
		`{"type":"checkout.session.completed","data":{"object":{"id":"nonexistent_ext_id","status":"paid"}}}`))
	if w.Code != http.StatusOK {
		t.Errorf("unknown external ID should ACK 200, got %d", w.Code)
	}
}

func TestIntegration_Webhook_IdempotentPaid(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_wh_idem_user"
		orgID = "00000000-0000-0000-0000-000000000f04"
		extID = "stripe_sess_f04"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	subID := getSubscriptionID(pool, orgID)
	invID := seedInvoice(pool, subID, "USD", 900)
	seedPaymentLink(pool, invID, extID, "stripe", "USD", 900)

	payload := `{"type":"checkout.session.completed","data":{"object":{"id":"` + extID + `","payment_status":"paid"}}}`
	e := billing.NewWebhookModuleEngine(pool, user, orgID)

	for range 2 {
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
		if w.Code != http.StatusOK {
			t.Fatalf("idempotent call: want 200, got %d: %s", w.Code, w.Body)
		}
	}

	var paymentCount int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.payments WHERE invoice_id = $1`, invID).Scan(&paymentCount)
	if paymentCount != 1 {
		t.Errorf("idempotent webhook: want exactly 1 payment record, got %d", paymentCount)
	}
}

// TestIntegration_Webhook_TransientFailureRollsBackMarker regression-tests
// that a webhook delivery which fails partway through its transaction rolls
// back its idempotency marker along with every write it made, so the
// provider's retry (same event ID) is fully reprocessed instead of being
// silently ACK'd as a duplicate with the invoice left unpaid forever.
func TestIntegration_Webhook_TransientFailureRollsBackMarker(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user          = "integ_billing_wh_transient_user"
		orgID         = "00000000-0000-0000-0000-000000000f09"
		extID         = "stripe_sess_f09"
		eventID       = "evt_f09_test"
		failingAmount = int64(999999999)
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	subID := getSubscriptionID(pool, orgID)
	invID := seedInvoice(pool, subID, "USD", failingAmount)
	seedPaymentLink(pool, invID, extID, "stripe", "USD", failingAmount)

	// Trigger simulates a transient DB failure partway through the webhook's
	// transaction (after the idempotency marker insert, during the payment write).
	if _, err := pool.Exec(t.Context(), `
		CREATE OR REPLACE FUNCTION billing.__test_reject_sentinel_payment() RETURNS trigger AS $$
		BEGIN
			IF NEW.amount_cents = 999999999 THEN
				RAISE EXCEPTION 'simulated transient failure';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create trigger function: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		CREATE TRIGGER __test_reject_sentinel_payment BEFORE INSERT ON billing.payments
		FOR EACH ROW EXECUTE FUNCTION billing.__test_reject_sentinel_payment()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS __test_reject_sentinel_payment ON billing.payments`)
		pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS billing.__test_reject_sentinel_payment()`)
		// cleanupBillingByOrganization's webhook_events delete only matches
		// event_id LIKE external_id || '%' (true for Xendit's convention,
		// where event_id embeds external_id). This test's event_id is a
		// realistic independent Stripe event ID, so it needs its own cleanup.
		pool.Exec(context.Background(), `DELETE FROM billing.webhook_events WHERE provider = 'stripe' AND event_id = $1`, eventID)
	})

	payload := `{"id":"` + eventID + `","type":"checkout.session.completed","data":{"object":{"id":"` + extID + `","payment_status":"paid"}}}`
	e := billing.NewWebhookModuleEngine(pool, user, orgID)

	// First delivery fails mid-transaction.
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("first delivery: want 500 (transient failure), got %d: %s", w.Code, w.Body)
	}

	var invStatus string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, invID).Scan(&invStatus)
	if invStatus != "pending" {
		t.Errorf("after failed delivery: want invoice still pending (rolled back), got %q", invStatus)
	}
	var markerCount int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.webhook_events WHERE provider = 'stripe' AND event_id = $1`, eventID).Scan(&markerCount)
	if markerCount != 0 {
		t.Errorf("after failed delivery: want idempotency marker rolled back, found %d", markerCount)
	}

	// Provider retries the exact same event once the transient condition clears.
	if _, err := pool.Exec(t.Context(), `DROP TRIGGER __test_reject_sentinel_payment ON billing.payments`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}

	w = httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if w.Code != http.StatusOK {
		t.Fatalf("retry: want 200, got %d: %s", w.Code, w.Body)
	}

	pool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, invID).Scan(&invStatus)
	if invStatus != "paid" {
		t.Errorf("after retry: want invoice paid, got %q", invStatus)
	}
	var paymentCount2 int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.payments WHERE invoice_id = $1`, invID).Scan(&paymentCount2)
	if paymentCount2 != 1 {
		t.Errorf("after retry: want exactly 1 payment record, got %d", paymentCount2)
	}
}

func TestIntegration_Webhook_PaidReactivatesExpiredSubscription(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_wh_reactivate_user"
		orgID = "00000000-0000-0000-0000-000000000f05"
		extID = "stripe_sess_f05"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET status = 'expired' WHERE subject_type = 'organization' AND subject_id = $1`, orgID)

	subID := getSubscriptionID(pool, orgID)
	invID := seedInvoice(pool, subID, "USD", 900)
	seedPaymentLink(pool, invID, extID, "stripe", "USD", 900)

	payload := `{"type":"checkout.session.completed","data":{"object":{"id":"` + extID + `","payment_status":"paid"}}}`
	e := billing.NewWebhookModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if w.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", w.Code, w.Body)
	}

	data := getSubscriptionData(t, billing.NewModuleEngine(pool, user, orgID), orgID)
	if data["status"] != "active" {
		t.Errorf("after paid webhook: want status=active, got %v", data["status"])
	}
}

// --- Payment link flow ---

func TestIntegration_CreatePaymentLink_InvoiceNotFound(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_pl_notfound_user"
		orgID = "00000000-0000-0000-0000-000000000f06"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	fakeInvoiceID := "00000000-0000-0000-0000-000000000000"
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost,
		billingURL(orgID)+"/invoices/"+fakeInvoiceID+"/pay", ""))
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown invoice: want 404, got %d", w.Code)
	}
}

// --- Usage metering ---

func TestIntegration_RecordAndGetUsage(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_usage_user"
		orgID = "00000000-0000-0000-0000-000000000a01"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID)

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/usage", `{"metric":"members","value":1}`))
	if w.Code != http.StatusNoContent {
		t.Fatalf("record usage: want 204, got %d: %s", w.Code, w.Body)
	}

	wGet := httptest.NewRecorder()
	e.ServeHTTP(wGet, httpserver.JSONTestRequest(http.MethodGet, billingURL(orgID)+"/usage", ""))
	if wGet.Code != http.StatusOK {
		t.Fatalf("get usage: want 200, got %d: %s", wGet.Code, wGet.Body)
	}
	var resp map[string]any
	json.NewDecoder(wGet.Body).Decode(&resp)
	if items, ok := resp["data"].([]any); !ok || len(items) == 0 {
		t.Error("expected at least one usage item after recording")
	}
}

// --- Billing workers ---

func TestIntegration_HandleSubscriptionRemind_ActiveSub(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_remind_user"
		orgID = "00000000-0000-0000-0000-000000000a02"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	var subID string
	pool.QueryRow(t.Context(),
		`SELECT id FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1`, orgID,
	).Scan(&subID)

	body := encodeSubscriptionCheckEvent(subID, orgID, time.Now().Add(3*24*time.Hour))
	if err := mod.Worker.HandleSubscriptionRemind(t.Context(), body); err != nil {
		t.Errorf("HandleSubscriptionRemind active sub: want nil, got %v", err)
	}
}

func TestIntegration_HandleSubscriptionAutoInvoice_InsertsInvoice(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_autoinv_user"
		orgID = "00000000-0000-0000-0000-000000000a03"
	)
	setupBillingTest(t, pool, orgID)

	// stubRefReader needed: HandleSubscriptionAutoInvoice calls GetPlanByID + GetCountryTaxRate
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	var subID string
	pool.QueryRow(t.Context(),
		`SELECT id FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1`, orgID,
	).Scan(&subID)

	body := encodeSubscriptionCheckEvent(subID, orgID, time.Now().Add(3*24*time.Hour))
	// createPaymentLink will fail with empty ProviderConfig — ignore the error
	_ = mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body)

	// invoice must be inserted before the payment-link call fails
	var invoiceCount int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.invoices WHERE subscription_id = $1`, subID,
	).Scan(&invoiceCount)
	if invoiceCount == 0 {
		t.Error("HandleSubscriptionAutoInvoice: invoice row must be inserted before payment link creation")
	}
}

// TestIntegration_HandleSubscriptionAutoInvoice_ZeroPricePlan_SkipsInvoice
// regression-tests that a subscription on a zero-priced plan ("custom",
// contact-us only — never self-service selectable, but reachable if one
// is ever attached by Studio) renews without error and without an invoice.
// insertInvoice's amount_cents CHECK requires > 0; composeInvoiceAmount
// would resolve exactly 0 here (no addons, no coupon), so the call site
// must skip invoicing instead of hitting that constraint.
func TestIntegration_HandleSubscriptionAutoInvoice_ZeroPricePlan_SkipsInvoice(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_autoinv_zero_user"
		orgID = "00000000-0000-0000-0000-000000000a10"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventWithPlan(orgID, user, "custom", "monthly")); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)

	body := encodeSubscriptionCheckEvent(subID, orgID, time.Now().Add(3*24*time.Hour))
	if err := mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body); err != nil {
		t.Fatalf("HandleSubscriptionAutoInvoice: want nil error for a zero-priced plan, got %v", err)
	}

	var invoiceCount int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.invoices WHERE subscription_id = $1`, subID,
	).Scan(&invoiceCount)
	if invoiceCount != 0 {
		t.Errorf("want no invoice for a zero-priced plan renewal, got %d", invoiceCount)
	}
}

// TestIntegration_HandleSubscriptionAutoInvoice_RetriesPaymentLinkOnRedelivery
// regression-tests that a redelivered auto-invoice event (the message
// consumer's retry after the first delivery's payment-link creation failed)
// retries the payment link for the existing pending invoice instead of
// silently no-op'ing because a pending invoice already exists, and that it
// never creates a second, duplicate invoice while doing so.
func TestIntegration_HandleSubscriptionAutoInvoice_RetriesPaymentLinkOnRedelivery(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_autoinv_retry_user"
		orgID = "00000000-0000-0000-0000-000000000a04"
	)
	setupBillingTest(t, pool, orgID)

	// stubRefReader needed: HandleSubscriptionAutoInvoice calls GetPlanByID + GetCountryTaxRate
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	body := encodeSubscriptionCheckEvent(subID, orgID, time.Now().Add(3*24*time.Hour))

	// First delivery: invoice is created, payment link fails (empty ProviderConfig).
	if err := mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body); err == nil {
		t.Fatal("first delivery: want an error from the failed payment-link creation")
	}

	var invoiceCountAfterFirst int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.invoices WHERE subscription_id = $1`, subID).Scan(&invoiceCountAfterFirst)
	if invoiceCountAfterFirst != 1 {
		t.Fatalf("want 1 invoice after first delivery, got %d", invoiceCountAfterFirst)
	}

	// Redelivery must retry the payment-link creation for the existing
	// pending invoice — it still fails with an empty ProviderConfig, but
	// that failure proves the attempt was made rather than skipped.
	if err := mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body); err == nil {
		t.Error("redelivery: want the payment-link creation retried (and still fail), not silently no-op'd")
	}

	var invoiceCountAfterRetry int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.invoices WHERE subscription_id = $1`, subID).Scan(&invoiceCountAfterRetry)
	if invoiceCountAfterRetry != 1 {
		t.Errorf("want still exactly 1 invoice (no duplicate) after redelivery, got %d", invoiceCountAfterRetry)
	}
}

// --- Webhook FAILED path ---

func TestIntegration_Webhook_Failed_MarksPastDue(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_wh_failed_user"
		orgID = "00000000-0000-0000-0000-000000000f08"
		extID = "xendit_inv_f08"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	// Force subscription to active so the past_due transition is exercisable.
	pool.Exec(t.Context(), `UPDATE billing.subscriptions SET status = 'active' WHERE subject_type = 'organization' AND subject_id = $1`, orgID)

	subID := getSubscriptionID(pool, orgID)
	invID := seedInvoice(pool, subID, "IDR", 150000)
	seedPaymentLink(pool, invID, extID, "xendit", "IDR", 150000)

	e := billing.NewWebhookModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/xendit",
		`{"id":"`+extID+`","status":"FAILED"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("FAILED webhook: want 200, got %d: %s", w.Code, w.Body)
	}

	var linkStatus string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.payment_links WHERE invoice_id = $1`, invID).Scan(&linkStatus)
	if linkStatus != "failed" {
		t.Errorf("payment_link: want status=failed, got %q", linkStatus)
	}

	var subStatus string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.subscriptions WHERE id = $1`, subID).Scan(&subStatus)
	if subStatus != "past_due" {
		t.Errorf("subscription: want status=past_due after FAILED webhook, got %q", subStatus)
	}
}

// --- checkUsageLimit exceeded ---

func TestIntegration_CheckUsageLimit_ExceededPath(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_usage_limit_user"
		orgID = "00000000-0000-0000-0000-000000000f09"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	// solo plan has members limit = 5; record exactly 5 (upsert overwrites)
	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/usage", `{"metric":"members","value":5}`))
	if w.Code != http.StatusNoContent {
		t.Fatalf("record usage: want 204, got %d: %s", w.Code, w.Body)
	}

	current, limit, err := mod.CheckUsageLimit(t.Context(), orgID, "members")
	if err != nil {
		t.Fatalf("CheckUsageLimit: %v", err)
	}
	if current < int64(limit) || limit < 0 {
		t.Errorf("want current (%d) >= limit (%d)", current, limit)
	}
}

// --- checkUsageLimit + attached addon deltas ---

func TestIntegration_CheckUsageLimit_IncludesAttachedAddonDelta(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_addon_delta_user"
		orgID = "00000000-0000-0000-0000-000000000f12"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	sub, err := mod.GetSubscriptionBySubject(t.Context(), "organization", orgID)
	if err != nil {
		t.Fatalf("GetSubscriptionBySubject: %v", err)
	}

	// Attach the real seeded "extra-seat" addon (+1 member/unit,
	// db/migrations/000004_billing.up.sql) directly at quantity 5 (+5
	// members total) — bypasses the HTTP attach route to seed
	// subscription_addons state directly, same as other integration tests
	// that reach into billing.* tables rather than go through the API.
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO billing.subscription_addons (subscription_id, addon_id, quantity) VALUES ($1, 'extra-seat', 5)`,
		sub.ID,
	); err != nil {
		t.Fatalf("attach addon: %v", err)
	}

	// solo plan grants members limit=1 (seed data); the real addon (quantity 5) adds +5.
	current, limit, err := mod.CheckUsageLimit(t.Context(), orgID, "members")
	if err != nil {
		t.Fatalf("CheckUsageLimit: %v", err)
	}
	if limit != 6 {
		t.Errorf("want limit=6 (plan 1 + addon 5), got %d (current=%d)", limit, current)
	}
}

// --- GET /billing/features entitlement resolution ---

func TestIntegration_ListFeatures_ReturnsResolvedEntitlements(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_entitlements_user"
		orgID = "00000000-0000-0000-0000-000000000f13"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	sub, err := mod.GetSubscriptionBySubject(t.Context(), "organization", orgID)
	if err != nil {
		t.Fatalf("GetSubscriptionBySubject: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO billing.subscription_addons (subscription_id, addon_id, quantity) VALUES ($1, 'extra-seat', 5)`,
		sub.ID,
	); err != nil {
		t.Fatalf("attach addon: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(orgID)+"/features", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp struct {
		Data []struct {
			FeatureID string `json:"feature_id"`
			Type      string `json:"type"`
			Limit     int    `json:"limit"`
			Current   int64  `json:"current"`
			Remaining int64  `json:"remaining"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	var found bool
	for _, e := range resp.Data {
		if e.FeatureID != "members" {
			continue
		}
		found = true
		if e.Type != "metered" {
			t.Errorf("members: want type=metered, got %q", e.Type)
		}
		if e.Limit != 6 {
			t.Errorf("members: want limit=6 (plan 1 + addon 5), got %d", e.Limit)
		}
		if e.Remaining != int64(e.Limit)-e.Current {
			t.Errorf("members: want remaining=limit-current, got remaining=%d limit=%d current=%d", e.Remaining, e.Limit, e.Current)
		}
	}
	if !found {
		t.Fatal("want a members entitlement in the response")
	}
}

// --- regeneratePaymentLink ---

func TestIntegration_RegeneratePaymentLink_ExpiresOldLink(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_regen_user"
		orgID = "00000000-0000-0000-0000-000000000f10"
		extID = "stripe_regen_f10"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	subID := getSubscriptionID(pool, orgID)
	invID := seedInvoice(pool, subID, "USD", 900)
	linkID := seedPaymentLink(pool, invID, extID, "stripe", "USD", 900)

	// regenerate: old link must be expired before createPaymentLink is attempted
	e := billing.NewModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost,
		billingURL(orgID)+"/invoices/"+invID+"/pay/regenerate", ""))
	// createPaymentLink will fail (empty ProviderConfig) → 500 expected; old link must be expired regardless
	_ = w.Code

	var linkStatus string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.payment_links WHERE id = $1`, linkID).Scan(&linkStatus)
	if linkStatus != "expired" {
		t.Errorf("old payment link: want status=expired after regenerate, got %q", linkStatus)
	}
}

// --- HandleOrganizationDeleted ---

func TestIntegration_HandleOrganizationDeleted_CancelsSubscription(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_ws_deleted_user"
		orgID = "00000000-0000-0000-0000-000000000f11"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	deletedEvt := events.Envelope{
		ID: "del-" + orgID, Type: events.RoutingKeyOrganizationDeleted,
		Source: "organization", Time: time.Now(), OrgID: orgID,
		Data: events.OrganizationDeleted{OrganizationID: orgID, DeletedAt: time.Now()},
	}
	body, _ := json.Marshal(deletedEvt)

	if err := mod.Worker.HandleOrganizationDeleted(t.Context(), body); err != nil {
		t.Fatalf("HandleOrganizationDeleted: %v", err)
	}

	var status string
	pool.QueryRow(t.Context(),
		`SELECT status FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1`, orgID,
	).Scan(&status)
	if status != "cancelled" {
		t.Errorf("after organization deleted: want status=cancelled, got %q", status)
	}
}

func TestIntegration_CreatePaymentLink_InvoiceAlreadyPaid(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_pl_paid_user"
		orgID = "00000000-0000-0000-0000-000000000f07"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	subID := getSubscriptionID(pool, orgID)
	invID := seedPaidInvoice(pool, subID, "USD", 900)

	e := billing.NewModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost,
		billingURL(orgID)+"/invoices/"+invID+"/pay", ""))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("paid invoice: want 422, got %d", w.Code)
	}
}

// --- coupon redemption ---

func couponAmount(v int64) *int64 { return new(v) }

func TestIntegration_RedeemCoupon_HappyPath_DiscountAppliesOnNextInvoice(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_coupon_happy_user"
		orgID = "00000000-0000-0000-0000-000000000f14"
		code  = "TESTCOUPON_F14"
	)
	setupBillingTest(t, pool, orgID)
	seedCoupon(t, pool, code, couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(200), Cadence: "once"})

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)

	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/coupons/redeem", `{"code":"`+code+`"}`))
	if w.Code != http.StatusNoContent {
		t.Fatalf("redeem: want 204, got %d: %s", w.Code, w.Body)
	}

	// GET /billing should now show the active coupon.
	w = httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(orgID), ""))
	var subResp struct {
		Data struct {
			ActiveCoupon *string `json:"active_coupon"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&subResp)
	if subResp.Data.ActiveCoupon == nil || *subResp.Data.ActiveCoupon != code {
		t.Errorf("GET /billing: want active_coupon=%q, got %v", code, subResp.Data.ActiveCoupon)
	}

	// Trigger a renewal auto-invoice — solo plan is $9/mo (900 cents, seed data).
	body := encodeSubscriptionCheckEvent(subID, orgID, time.Now().Add(3*24*time.Hour))
	_ = mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body) // payment link creation fails with empty ProviderConfig, ignore

	var invID string
	var amountCents int64
	pool.QueryRow(t.Context(),
		`SELECT id, amount_cents FROM billing.invoices WHERE subscription_id = $1 ORDER BY created_at DESC LIMIT 1`, subID,
	).Scan(&invID, &amountCents)
	if amountCents != 700 { // 900 plan - 200 discount
		t.Errorf("invoice amount: want 700 (900-200 discount), got %d", amountCents)
	}

	var discountLineCount int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.invoice_line_items WHERE invoice_id = $1 AND total_cents < 0`, invID,
	).Scan(&discountLineCount)
	if discountLineCount != 1 {
		t.Errorf("want 1 negative discount line item, got %d", discountLineCount)
	}

	var appliedCount int
	pool.QueryRow(t.Context(),
		`SELECT applied_count FROM billing.coupon_redemptions WHERE coupon_id = $1 AND subscription_id = $2`, code, subID,
	).Scan(&appliedCount)
	if appliedCount != 1 {
		t.Errorf("want applied_count=1 after one invoice, got %d", appliedCount)
	}
}

func TestIntegration_RedeemCoupon_RejectionCases(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_coupon_reject_user"
		orgID = "00000000-0000-0000-0000-000000000f15"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	past := time.Now().Add(-24 * time.Hour)
	zero := 0
	seedCoupon(t, pool, "EXPIRED_F15", couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(100), ValidUntil: &past})
	seedCoupon(t, pool, "MAXEDOUT_F15", couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(100), MaxRedemptions: &zero})
	seedCoupon(t, pool, "OTHERORG_F15", couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(100)})
	pool.Exec(t.Context(), `INSERT INTO billing.coupon_targets (coupon_id, subject_type, subject_id) VALUES ($1, 'organization', $2)`,
		"OTHERORG_F15", "00000000-0000-0000-0000-000000000000")

	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})
	cases := []struct {
		name string
		code string
	}{
		{"unknown code", "NOPE_F15"},
		{"expired", "EXPIRED_F15"},
		{"max redemptions reached", "MAXEDOUT_F15"},
		{"targeted at a different organization", "OTHERORG_F15"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/coupons/redeem", `{"code":"`+c.code+`"}`))
			if w.Code != http.StatusNotFound && w.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s: want 404 or 422, got %d: %s", c.name, w.Code, w.Body)
			}
		})
	}
}

func TestIntegration_RedeemCoupon_AlreadyHasActiveCoupon_Rejected(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_coupon_stack_user"
		orgID = "00000000-0000-0000-0000-000000000f16"
	)
	setupBillingTest(t, pool, orgID)
	seedCoupon(t, pool, "FIRST_F16", couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(100), Cadence: "forever"})
	seedCoupon(t, pool, "SECOND_F16", couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(100), Cadence: "forever"})

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/coupons/redeem", `{"code":"FIRST_F16"}`))
	if w.Code != http.StatusNoContent {
		t.Fatalf("first redeem: want 204, got %d: %s", w.Code, w.Body)
	}

	w = httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/coupons/redeem", `{"code":"SECOND_F16"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("second redeem while first still active: want 422, got %d: %s", w.Code, w.Body)
	}
}

// TestIntegration_RedeemCoupon_ConcurrentRedemptions_OnlyOneWins regression-tests
// that two concurrent redemptions for the same subscription must not both
// succeed. Uses NewModuleEngineWithRealRLS (unlike every other test in this
// file) because the race only reproduces under a real per-request
// transaction — the fix locks the subscription row for the rest of that
// transaction so the second request's check blocks until the first commits.
func TestIntegration_RedeemCoupon_ConcurrentRedemptions_OnlyOneWins(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_coupon_race_user"
		orgID = "00000000-0000-0000-0000-000000000f31"
	)
	setupBillingTest(t, pool, orgID)
	seedCoupon(t, pool, "RACE_A_F31", couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(100), Cadence: "forever"})
	seedCoupon(t, pool, "RACE_B_F31", couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(100), Cadence: "forever"})

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)

	e := billing.NewModuleEngineWithRealRLS(pool, user, orgID, stubRefReader{})

	codes := []string{"RACE_A_F31", "RACE_B_F31"}
	results := make([]int, len(codes))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, code := range codes {
		wg.Add(1)
		go func(i int, code string) {
			defer wg.Done()
			<-start
			w := httptest.NewRecorder()
			e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/coupons/redeem", `{"code":"`+code+`"}`))
			results[i] = w.Code
		}(i, code)
	}
	close(start)
	wg.Wait()

	successCount := 0
	for _, status := range results {
		if status == http.StatusNoContent {
			successCount++
		}
	}
	if successCount != 1 {
		t.Errorf("want exactly 1 of 2 concurrent redemptions to succeed, got %d (statuses: %v)", successCount, results)
	}

	var redemptionCount int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.coupon_redemptions WHERE subscription_id = $1`, subID).Scan(&redemptionCount)
	if redemptionCount != 1 {
		t.Errorf("want exactly 1 redemption row after the race, got %d", redemptionCount)
	}
}

// --- addon attach/detach ---

func TestIntegration_AttachDetachAddon_ReflectsInListAndInvoice(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_addon_attach_user"
		orgID = "00000000-0000-0000-0000-000000000f17"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)

	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/addons", `{"addon_id":"extra-seat","quantity":5}`))
	if w.Code != http.StatusNoContent {
		t.Fatalf("attach: want 204, got %d: %s", w.Code, w.Body)
	}

	w = httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(orgID)+"/addons", ""))
	var listResp struct {
		Data []struct {
			AddonID string `json:"addon_id"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&listResp)
	if len(listResp.Data) != 1 || listResp.Data[0].AddonID != "extra-seat" {
		t.Errorf("GET /billing/addons: want 1 attached addon extra-seat, got %+v", listResp.Data)
	}

	// Renewal invoice should include the addon's price on top of the plan price.
	body := encodeSubscriptionCheckEvent(subID, orgID, time.Now().Add(3*24*time.Hour))
	_ = mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body)

	// The addon's price comes straight from billing.addons (composeInvoiceAmount
	// bypasses the catalog abstraction for addon pricing), and that row is
	// Studio-editable — read it back rather than assuming the migration's
	// seed value, so this test survives a catalog price edit.
	var addonMonthlyCents int64
	pool.QueryRow(t.Context(),
		`SELECT (prices->'USD'->>'monthly')::bigint FROM billing.addons WHERE id = 'extra-seat'`,
	).Scan(&addonMonthlyCents)
	wantAmount := int64(900) + addonMonthlyCents*5 // solo plan is 900 (seed data)

	var amountCents int64
	pool.QueryRow(t.Context(),
		`SELECT amount_cents FROM billing.invoices WHERE subscription_id = $1 ORDER BY created_at DESC LIMIT 1`, subID,
	).Scan(&amountCents)
	if amountCents != wantAmount {
		t.Errorf("invoice amount with addon: want %d (plan 900 + addon %d x5), got %d", wantAmount, addonMonthlyCents, amountCents)
	}

	w = httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodDelete, billingURL(orgID)+"/addons/extra-seat", ""))
	if w.Code != http.StatusNoContent {
		t.Fatalf("detach: want 204, got %d: %s", w.Code, w.Body)
	}

	w = httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(orgID)+"/addons", ""))
	listResp.Data = nil
	json.NewDecoder(w.Body).Decode(&listResp)
	if len(listResp.Data) != 0 {
		t.Errorf("after detach: want 0 attached addons, got %d", len(listResp.Data))
	}
}

func TestIntegration_AttachAddon_UnknownAddon_NotFound(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_addon_unknown_user"
		orgID = "00000000-0000-0000-0000-000000000f18"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/addons", `{"addon_id":"does-not-exist"}`))
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown addon: want 404, got %d: %s", w.Code, w.Body)
	}
}

// --- onboarding plan selection ---

func TestIntegration_HandleOrganizationCreated_UsesEventPlan(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_eventplan_user"
		orgID = "00000000-0000-0000-0000-000000000f19"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	body := encodeOrganizationCreatedEventWithPlan(orgID, user, "growth", "yearly")
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), body); err != nil {
		t.Fatalf("provision: %v", err)
	}

	var plan, cycle, status string
	pool.QueryRow(t.Context(),
		`SELECT plan, cycle, status FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1`, orgID,
	).Scan(&plan, &cycle, &status)
	if plan != "growth" {
		t.Errorf("want plan=growth (from event, not hardcoded solo), got %q", plan)
	}
	if cycle != "yearly" {
		t.Errorf("want cycle=yearly (from event, not hardcoded monthly), got %q", cycle)
	}
	if status != "trialing" {
		t.Errorf("want status=trialing (first organization for this owner), got %q", status)
	}
}

// Plan and cycle are required at the API boundary (organization.
// createOrganizationRequest) — no defaulting happens downstream anymore. An
// empty plan/cycle reaching the worker (a malformed or pre-contract event)
// must fail loudly instead of silently falling back to solo/monthly: plan is
// FK-constrained to billing.plans(id), cycle is CHECK-constrained to
// ('monthly', 'yearly').
func TestIntegration_HandleOrganizationCreated_EmptyPlanOrCycle_ReturnsError(t *testing.T) {
	pool := testPoolBilling(t)

	t.Run("empty plan", func(t *testing.T) {
		const (
			user  = "integ_billing_emptyplan_user"
			orgID = "00000000-0000-0000-0000-000000000f20"
		)
		setupBillingTest(t, pool, orgID)
		mod := billing.NewModuleForTest(pool, stubRefReader{})
		body := encodeOrganizationCreatedEventWithPlan(orgID, user, "", "monthly")
		if err := mod.Worker.HandleOrganizationCreated(t.Context(), body); err == nil {
			t.Fatal("want error for empty plan, got nil")
		}
	})

	t.Run("empty cycle", func(t *testing.T) {
		const (
			user  = "integ_billing_emptycycle_user"
			orgID = "00000000-0000-0000-0000-000000000f21"
		)
		setupBillingTest(t, pool, orgID)
		mod := billing.NewModuleForTest(pool, stubRefReader{})
		body := encodeOrganizationCreatedEventWithPlan(orgID, user, "solo", "")
		if err := mod.Worker.HandleOrganizationCreated(t.Context(), body); err == nil {
			t.Fatal("want error for empty cycle, got nil")
		}
	})
}

// --- subscription extension ---

func TestIntegration_ExtendSubscription_HappyPath(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_extend_user"
		wsID1 = "00000000-0000-0000-0000-000000000f21"
		wsID2 = "00000000-0000-0000-0000-000000000f22"
	)
	cleanupBillingByOrganization(pool, wsID1)
	cleanupBillingByOrganization(pool, wsID2)
	t.Cleanup(func() {
		cleanupBillingByOrganization(pool, wsID1)
		cleanupBillingByOrganization(pool, wsID2)
	})
	seedBillingOrganization(pool, wsID1, user)
	seedBillingOrganization(pool, wsID2, user)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(organization.New(pool, messaging.NoopPublisher{}))
	// wsID1 takes the trial; wsID2 (this test's subject) provisions
	// straight to "active" — extension only applies to active subscriptions.
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID2, user)); err != nil {
		t.Fatalf("second provision: %v", err)
	}

	beforePeriodEnd := new(string)
	pool.QueryRow(t.Context(), `SELECT period_end FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1`, wsID2).Scan(beforePeriodEnd)

	e := billing.NewModuleEngine(pool, user, wsID2, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"months":1}`))
	if w.Code != http.StatusOK {
		t.Fatalf("extend: want 200, got %d: %s", w.Code, w.Body)
	}

	var resp struct {
		Data struct {
			AmountCents int64 `json:"amount_cents"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Data.AmountCents != 900 { // solo plan: $9/mo monthly price (seed data)
		t.Errorf("extend invoice: want amount_cents=900 (1 month at solo's $9/mo), got %d", resp.Data.AmountCents)
	}

	data := getSubscriptionData(t, e, wsID2)
	if data["period_end"] == *beforePeriodEnd {
		t.Error("want period_end to move forward after extension")
	}

	var historyCount int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.subscription_history WHERE subscription_id = (SELECT id FROM billing.subscriptions WHERE subject_id = $1) AND action = 'extend'`, wsID2,
	).Scan(&historyCount)
	if historyCount != 1 {
		t.Errorf("want 1 'extend' history row, got %d", historyCount)
	}
}

// TestIntegration_ExtendSubscription_AtomicOnMidSequenceFailure regression-tests
// extendSubscription's own db.WithTx wrapping: this route doesn't run under
// the group-level RLS transaction (removed from billing/module.go so its
// Stripe/Xendit call never holds a pooled DB connection open), so it must
// wrap its own DB writes in an explicit transaction. A failure on the last
// write (history) must still roll back the earlier ones (invoice, line item)
// in the same sequence.
func TestIntegration_ExtendSubscription_AtomicOnMidSequenceFailure(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "SENTINEL_FAIL_TRIGGER"
		wsID1 = "00000000-0000-0000-0000-000000000f29"
		wsID2 = "00000000-0000-0000-0000-000000000f30"
	)
	cleanupBillingByOrganization(pool, wsID1)
	cleanupBillingByOrganization(pool, wsID2)
	t.Cleanup(func() {
		cleanupBillingByOrganization(pool, wsID1)
		cleanupBillingByOrganization(pool, wsID2)
	})
	seedBillingOrganization(pool, wsID1, user)
	seedBillingOrganization(pool, wsID2, user)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(organization.New(pool, messaging.NoopPublisher{}))
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID2, user)); err != nil {
		t.Fatalf("second provision: %v", err)
	}

	// Trigger simulates a failure on the transaction's last write
	// (insertHistory) to prove the earlier writes (invoice, line item,
	// period update) in the same transaction roll back with it.
	if _, err := pool.Exec(t.Context(), `
		CREATE OR REPLACE FUNCTION billing.__test_reject_sentinel_history() RETURNS trigger AS $$
		BEGIN
			IF NEW.changed_by = 'SENTINEL_FAIL_TRIGGER' THEN
				RAISE EXCEPTION 'simulated mid-sequence failure';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create trigger function: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		CREATE TRIGGER __test_reject_sentinel_history BEFORE INSERT ON billing.subscription_history
		FOR EACH ROW EXECUTE FUNCTION billing.__test_reject_sentinel_history()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS __test_reject_sentinel_history ON billing.subscription_history`)
		pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS billing.__test_reject_sentinel_history()`)
	})

	// wsID2 (not the first organization owned by user) is provisioned
	// straight to "active" with its own initial invoice — count baselines
	// before the extend attempt so the assertion below only catches a leak
	// from the failed extend itself, not the pre-existing provisioning invoice.
	invoiceCountBefore := countInvoices(pool, t, wsID2)
	lineItemCountBefore := countInvoiceLineItems(pool, t, wsID2)

	e := billing.NewModuleEngine(pool, user, wsID2, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"months":1}`))
	if w.Code == http.StatusOK {
		t.Fatalf("extend: want a failure status, got 200: %s", w.Body)
	}

	if got := countInvoices(pool, t, wsID2); got != invoiceCountBefore {
		t.Errorf("want the extension's invoice rolled back with the rest of the transaction, invoice count before=%d after=%d", invoiceCountBefore, got)
	}
	if got := countInvoiceLineItems(pool, t, wsID2); got != lineItemCountBefore {
		t.Errorf("want the extension's line item rolled back too, line item count before=%d after=%d", lineItemCountBefore, got)
	}
}

func countInvoices(pool *pgxpool.Pool, t *testing.T, wsID string) int {
	t.Helper()
	var n int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.invoices i JOIN billing.subscriptions s ON s.id = i.subscription_id WHERE s.subject_id = $1`, wsID,
	).Scan(&n)
	return n
}

func countInvoiceLineItems(pool *pgxpool.Pool, t *testing.T, wsID string) int {
	t.Helper()
	var n int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.invoice_line_items li JOIN billing.invoices i ON i.id = li.invoice_id JOIN billing.subscriptions s ON s.id = i.subscription_id WHERE s.subject_id = $1`, wsID,
	).Scan(&n)
	return n
}

func TestIntegration_ExtendSubscription_ExceedsMaxDuration_Rejected(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_extendcap_user"
		wsID1 = "00000000-0000-0000-0000-000000000f23"
		wsID2 = "00000000-0000-0000-0000-000000000f24"
	)
	cleanupBillingByOrganization(pool, wsID1)
	cleanupBillingByOrganization(pool, wsID2)
	t.Cleanup(func() {
		cleanupBillingByOrganization(pool, wsID1)
		cleanupBillingByOrganization(pool, wsID2)
	})
	seedBillingOrganization(pool, wsID1, user)
	seedBillingOrganization(pool, wsID2, user)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(organization.New(pool, messaging.NoopPublisher{}))
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID2, user)); err != nil {
		t.Fatalf("second provision: %v", err)
	}

	// Simulate a subscription created 23 months ago: created_at+2y = now+1mo,
	// and period_end is already now+1mo (fresh active sub) — so a further
	// 1-month extension would land at now+2mo, past the cap.
	pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET created_at = now() - interval '23 months' WHERE subject_type = 'organization' AND subject_id = $1`,
		wsID2)

	e := billing.NewModuleEngine(pool, user, wsID2, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"months":1}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("extend past 2y cap: want 422, got %d: %s", w.Code, w.Body)
	}
}

func TestIntegration_ExtendSubscription_Trialing_Rejected(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_extendtrial_user"
		orgID = "00000000-0000-0000-0000-000000000f25"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/extend", `{"months":1}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("extend while trialing: want 422, got %d: %s", w.Code, w.Body)
	}
}

// --- skip trial, activate + invoice now ---

func TestIntegration_ActivateTrialNow_HappyPath(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_activatenow_user"
		orgID = "00000000-0000-0000-0000-000000000f26"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/activate", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("activate: want 200, got %d: %s", w.Code, w.Body)
	}

	var resp struct {
		Data struct {
			AmountCents int64 `json:"amount_cents"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Data.AmountCents != 900 { // solo plan: $9/mo monthly price (seed data)
		t.Errorf("activate invoice: want amount_cents=900, got %d", resp.Data.AmountCents)
	}

	var status string
	var trialEnd *time.Time
	subID := getSubscriptionID(pool, orgID)
	pool.QueryRow(t.Context(), `SELECT status, trial_end FROM billing.subscriptions WHERE id = $1`, subID).Scan(&status, &trialEnd)
	if status != "active" {
		t.Errorf("want status=active after activate-now, got %q", status)
	}
	if trialEnd != nil {
		t.Error("want trial_end cleared after activate-now (else expireIfDue would wrongly expire this later)")
	}
}

func TestIntegration_ActivateTrialNow_NotTrialing_Rejected(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_activatenow_reject_user"
		orgID = "00000000-0000-0000-0000-000000000f27"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})
	// Already-active (via cancel+resume-after-trial-expired trick is
	// overkill here) — simplest: cancel while trialing, which flips status
	// to "cancelled", also not "trialing".
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel", ""))

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/activate", ""))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("activate while not trialing: want 422, got %d: %s", w.Code, w.Body)
	}
}

// --- previewInvoice / listEligibleCoupons ---

func TestIntegration_PreviewInvoice_CurrentState(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_preview_current_user"
		wsID1 = "00000000-0000-0000-0000-000000000f28"
		wsID2 = "00000000-0000-0000-0000-000000000f29"
	)
	cleanupBillingByOrganization(pool, wsID1)
	cleanupBillingByOrganization(pool, wsID2)
	t.Cleanup(func() {
		cleanupBillingByOrganization(pool, wsID1)
		cleanupBillingByOrganization(pool, wsID2)
	})
	seedBillingOrganization(pool, wsID1, user)
	seedBillingOrganization(pool, wsID2, user)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(organization.New(pool, messaging.NoopPublisher{}))
	// wsID1 takes the trial; wsID2 provisions straight to "active" — the
	// preview's proration branch only applies to a non-trialing subscription.
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID2, user)); err != nil {
		t.Fatalf("second provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, wsID2, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(wsID2)+"/preview", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("preview: want 200, got %d: %s", w.Code, w.Body)
	}

	var resp struct {
		Data struct {
			Plan         string  `json:"plan"`
			TotalCents   int64   `json:"total_cents"`
			NewPeriodEnd *string `json:"new_period_end"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Data.Plan != "solo" {
		t.Errorf("preview current state: want plan=solo, got %q", resp.Data.Plan)
	}
	if resp.Data.TotalCents != 900 { // solo plan: $9/mo (seed data)
		t.Errorf("preview current state: want total_cents=900, got %d", resp.Data.TotalCents)
	}
	if resp.Data.NewPeriodEnd != nil {
		t.Errorf("preview current state: want no new_period_end (not a plan change), got %v", *resp.Data.NewPeriodEnd)
	}
}

func TestIntegration_PreviewInvoice_HypotheticalPlanChange_SetsNewPeriodEnd(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_preview_hypo_user"
		wsID1 = "00000000-0000-0000-0000-000000000f30"
		wsID2 = "00000000-0000-0000-0000-000000000f31"
	)
	cleanupBillingByOrganization(pool, wsID1)
	cleanupBillingByOrganization(pool, wsID2)
	t.Cleanup(func() {
		cleanupBillingByOrganization(pool, wsID1)
		cleanupBillingByOrganization(pool, wsID2)
	})
	seedBillingOrganization(pool, wsID1, user)
	seedBillingOrganization(pool, wsID2, user)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(organization.New(pool, messaging.NoopPublisher{}))
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID2, user)); err != nil {
		t.Fatalf("second provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, wsID2, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(wsID2)+"/preview?plan=growth&cycle=monthly", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("preview: want 200, got %d: %s", w.Code, w.Body)
	}

	var resp struct {
		Data struct {
			Plan         string  `json:"plan"`
			TotalCents   int64   `json:"total_cents"`
			NewPeriodEnd *string `json:"new_period_end"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Data.Plan != "growth" {
		t.Errorf("preview hypothetical: want plan=growth, got %q", resp.Data.Plan)
	}
	if resp.Data.TotalCents != 2900 { // growth plan: $29/mo (seed data)
		t.Errorf("preview hypothetical: want total_cents=2900, got %d", resp.Data.TotalCents)
	}
	if resp.Data.NewPeriodEnd == nil {
		t.Error("preview hypothetical plan change: want new_period_end to be set")
	}

	// Nothing should actually have changed — preview never writes.
	data := getSubscriptionData(t, e, wsID2)
	if data["plan"] != "solo" {
		t.Errorf("preview must not mutate the subscription: want plan still solo, got %v", data["plan"])
	}
}

func TestIntegration_ListEligibleCoupons_FiltersByTargetAndValidity(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user             = "integ_billing_eligible_coupons_user"
		orgID            = "00000000-0000-0000-0000-000000000f32"
		otherOrgID       = "00000000-0000-0000-0000-000000000f33"
		globalCode       = "GLOBAL_F32"
		targetedOtherOrg = "TARGETED_OTHER_F32"
		expiredCode      = "EXPIRED_F32"
	)
	setupBillingTest(t, pool, orgID)
	seedCoupon(t, pool, globalCode, couponSeedOpts{DiscountType: "percent", PercentOff: percentOff(10), Cadence: "once"})
	past := time.Now().Add(-24 * time.Hour)
	seedCoupon(t, pool, expiredCode, couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(100), Cadence: "once", ValidUntil: &past})
	seedCoupon(t, pool, targetedOtherOrg, couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(100), Cadence: "once"})
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO billing.coupon_targets (coupon_id, subject_type, subject_id) VALUES ($1, 'organization', $2)`,
		targetedOtherOrg, otherOrgID,
	); err != nil {
		t.Fatalf("seed coupon target: %v", err)
	}

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(orgID)+"/coupons", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("list eligible coupons: want 200, got %d: %s", w.Code, w.Body)
	}

	var resp struct {
		Data []struct {
			Code string `json:"code"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)

	found := map[string]bool{}
	for _, c := range resp.Data {
		found[c.Code] = true
	}
	if !found[globalCode] {
		t.Errorf("want untargeted active coupon %q in eligible list, got %+v", globalCode, resp.Data)
	}
	if found[expiredCode] {
		t.Errorf("want expired coupon %q excluded from eligible list", expiredCode)
	}
	if found[targetedOtherOrg] {
		t.Errorf("want coupon %q targeted at a different organization excluded from this organization's eligible list", targetedOtherOrg)
	}
}

func percentOff(v int16) *int16 { return new(v) }
