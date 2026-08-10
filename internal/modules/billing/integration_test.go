package billing_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// stubRefReader satisfies contracts.CountryTaxReader — the only outside-schema
// concern billing consumes through an injected reader. Catalog data is
// billing's own schema, read via its own repo, so tests exercise real seed
// prices (db/migrations/000004_billing.up.sql) rather than a fixture.
type stubRefReader struct{}

func (stubRefReader) GetCountryTaxRate(_ context.Context, _ string) (int, error) { return 0, nil }

// stubOrgSuspender drives the auto-invoice worker's cancellation-reconciliation
// tests: it records every SuspendOrganization call and can be told to fail
// its first N calls before succeeding, to simulate a failed attempt
// followed by a successful redelivery.
type stubOrgSuspender struct {
	failCount int
	calls     []struct{ orgID, reason string }
}

func (s *stubOrgSuspender) SuspendOrganization(_ context.Context, orgID, reason string) error {
	s.calls = append(s.calls, struct{ orgID, reason string }{orgID, reason})
	if s.failCount > 0 {
		s.failCount--
		return errors.New("stub: suspend organization failed")
	}
	return nil
}

func (s *stubOrgSuspender) UnsuspendOrganization(_ context.Context, _ string) error { return nil }

// spyOrgCommander counts ResolveDowngradeOverage calls and can be told to
// fail its first N before succeeding — used both to assert exactly how many
// times the combined scheduled-overage check invokes it (never twice for one
// renewal, even when a plan downgrade and an addon decrease are scheduled
// together) and that a failed call leaves nothing half-applied, mirroring
// stubOrgSuspender's failCount shape for the same reason.
type spyOrgCommander struct {
	calls      int
	failCount  int
	resolution contracts.OverageResolution
}

func (s *spyOrgCommander) ResolveDowngradeOverage(
	_ context.Context, _ string, _ []string, _ int, _ bool,
) (contracts.OverageResolution, error) {
	s.calls++
	if s.failCount > 0 {
		s.failCount--
		return contracts.OverageResolution{}, errors.New("stub: resolve downgrade overage failed")
	}
	return s.resolution, nil
}

// seedScheduledPlanDowngrade sets subID's scheduled_plan/scheduled_cycle/
// scheduled_requested_at directly, to solo/monthly — every scheduled-overage
// test needs a downgrade whose target has a lower member limit than growth,
// and solo/monthly is the only one exercised so far. billing.subscriptions'
// own CHECK constraint requires all three columns set together or all three
// NULL, so seeding only a scheduled downgrade (without going through the
// HTTP downgrade flow) must set every one of them in the same statement.
func seedScheduledPlanDowngrade(t *testing.T, pool *pgxpool.Pool, subID string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		UPDATE billing.subscriptions
		SET scheduled_plan = 'solo', scheduled_cycle = 'monthly', scheduled_requested_at = now()
		WHERE id = $1`,
		subID); err != nil {
		t.Fatalf("seed scheduled plan downgrade: %v", err)
	}
}

// seedAddonWithScheduledQuantity attaches addonID at liveQuantity and
// schedules scheduledQuantity to apply at renewal (0 meaning "remove this
// addon at renewal") — the same all-or-nothing CHECK constraint as above
// applies to scheduled_quantity/scheduled_requested_at.
func seedAddonWithScheduledQuantity(t *testing.T, pool *pgxpool.Pool, subID, addonID string, liveQuantity, scheduledQuantity int) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO billing.subscription_addons (subscription_id, addon_id, quantity, scheduled_quantity, scheduled_requested_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (subscription_id, addon_id)
		DO UPDATE SET quantity = $3, scheduled_quantity = $4, scheduled_requested_at = now()`,
		subID, addonID, liveQuantity, scheduledQuantity); err != nil {
		t.Fatalf("seed addon with scheduled quantity: %v", err)
	}
}

// seedMemberUsage records billing.usage for the "members" metric directly —
// the overage-reconciliation tests need a specific current-usage number in
// place before the worker runs, without the HTTP round trip
// TestIntegration_PreviewInvoice_Downgrade_IncludesOverage uses.
func seedMemberUsage(t *testing.T, pool *pgxpool.Pool, orgID string, value int) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO billing.usage (organization_id, metric, value, period_start, period_end, recorded_at)
		VALUES ($1, 'members', $2, now(), now() + interval '1 month', now())`,
		orgID, value); err != nil {
		t.Fatalf("seed member usage: %v", err)
	}
}

// capturingPublisher records every published event body instead of sending
// it anywhere — used to assert HandleSubscriptionAutoInvoice's cancellation
// branch actually publishes SubscriptionCancelled (and only after its
// transaction commits), since there's no broker in this test binary.
type capturingPublisher struct {
	published []capturedEvent
}

type capturedEvent struct {
	exchange, routingKey string
	body                 []byte
}

func (p *capturingPublisher) Publish(_ context.Context, exchange, routingKey string, body []byte) error {
	p.published = append(p.published, capturedEvent{exchange, routingKey, body})
	return nil
}

func (p *capturingPublisher) PublishDelayed(_ context.Context, exchange, routingKey string, body []byte, _ time.Duration) error {
	return p.Publish(context.Background(), exchange, routingKey, body)
}

// syncCapturingPublisher is capturingPublisher's concurrency-safe twin, for
// tests that deliberately drive two webhook requests through the same
// engine from separate goroutines at once (capturingPublisher's plain slice
// append isn't safe for that).
type syncCapturingPublisher struct {
	mu        sync.Mutex
	published []capturedEvent
}

func (p *syncCapturingPublisher) Publish(_ context.Context, exchange, routingKey string, body []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.published = append(p.published, capturedEvent{exchange, routingKey, body})
	return nil
}

func (p *syncCapturingPublisher) PublishDelayed(ctx context.Context, exchange, routingKey string, body []byte, _ time.Duration) error {
	return p.Publish(ctx, exchange, routingKey, body)
}

func (p *syncCapturingPublisher) snapshot() []capturedEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.published)
}

// encodeOrganizationCreatedEventFor builds an event body for a specific
// createdBy subject, with plan="solo"/cycle="monthly" — plan and cycle are
// required at the API boundary, so no test exercising ordinary
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
// isTrial is variadic (default false) so the two HandleSubscriptionCheck
// callers, which don't care about it, don't need updating.
func encodeSubscriptionCheckEvent(subID, subjectID string, expectedEnd time.Time, isTrial ...bool) []byte {
	env := events.Envelope{
		ID: "check-" + subID, Type: events.RoutingKeySubscriptionCheck,
		Source: "billing", Time: time.Now(), OrgID: subjectID,
		Data: events.SubscriptionCheck{
			SubscriptionID: subID, SubjectType: "organization",
			SubjectID: subjectID, ExpectedEnd: expectedEnd, IsTrial: len(isTrial) > 0 && isTrial[0],
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

// TestIntegration_OrgScopedCatalog_ScopesToSubscriptionCurrency provisions a
// subscription whose country resolves to IDR and asserts both org-scoped
// catalog endpoints (GET .../billing/plans/catalog, GET .../billing/addons/
// catalog) return prices keyed by IDR only — never USD, and never every
// currency the catalog stores. The frontend's existing-subscription
// components read this org-scoped catalog rather than the GeoIP-resolved
// pre-org-creation one, since an existing org's currency is already fixed.
func TestIntegration_OrgScopedCatalog_ScopesToSubscriptionCurrency(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_catalog_idr_user"
		orgID = "00000000-0000-0000-0000-000000000f30"
	)
	setupBillingTest(t, pool, orgID)

	env := events.Envelope{
		ID: "evt-" + orgID, Type: events.RoutingKeyOrganizationCreated,
		Source: "organization", Time: time.Now(), OrgID: orgID,
		Data: events.OrganizationCreated{
			OrganizationID: orgID, Slug: "ws-idr-catalog", Name: "IDR Catalog Test",
			CreatedBy: user, CountryCode: "ID", Plan: "solo", Cycle: "monthly",
			CreatedAt: time.Now(),
		},
	}
	body, _ := json.Marshal(env)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), body); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})

	type catalogEntry struct {
		Prices map[string]json.RawMessage `json:"prices"`
	}

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(orgID)+"/plans/catalog", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("GET plans/catalog: want 200, got %d: %s", w.Code, w.Body)
	}
	var plansResp struct {
		Data []catalogEntry `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&plansResp)
	if len(plansResp.Data) == 0 {
		t.Fatal("GET plans/catalog: want at least one plan, got none")
	}
	for _, p := range plansResp.Data {
		if _, ok := p.Prices["IDR"]; !ok {
			t.Errorf("plans/catalog: want IDR-only prices for an IDR subscription, got %v", p.Prices)
		}
		if _, ok := p.Prices["USD"]; ok {
			t.Errorf("plans/catalog: want no USD entry for an IDR subscription, got %v", p.Prices)
		}
	}

	w = httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(orgID)+"/addons/catalog", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("GET addons/catalog: want 200, got %d: %s", w.Code, w.Body)
	}
	var addonsResp struct {
		Data []catalogEntry `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&addonsResp)
	if len(addonsResp.Data) == 0 {
		t.Fatal("GET addons/catalog: want at least one addon, got none")
	}
	for _, a := range addonsResp.Data {
		if _, ok := a.Prices["IDR"]; !ok {
			t.Errorf("addons/catalog: want IDR-only prices for an IDR subscription, got %v", a.Prices)
		}
		if _, ok := a.Prices["USD"]; ok {
			t.Errorf("addons/catalog: want no USD entry for an IDR subscription, got %v", a.Prices)
		}
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
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel", `{"reason":"too_expensive"}`))
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
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel", `{"reason":"too_expensive"}`))

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel", `{"reason":"too_expensive"}`))
	if w.Code == http.StatusOK {
		t.Errorf("double cancel should fail, got 200")
	}
}

// TestIntegration_CancelSubscription_RecordsReasonInHistory posts a real
// request through the full handler->service->DB stack and asserts the
// reason actually lands in subscription_history.metadata — not a mocked
// network layer, and not just that the handler accepted the shape.
func TestIntegration_CancelSubscription_RecordsReasonInHistory(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_cancelreason_user"
		orgID = "00000000-0000-0000-0000-000000000d13"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel", `{"reason":"missing_features"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("cancel: want 200, got %d: %s", w.Code, w.Body)
	}

	var metadataRaw []byte
	if err := pool.QueryRow(t.Context(),
		`SELECT metadata FROM billing.subscription_history WHERE subscription_id=(SELECT id FROM billing.subscriptions WHERE subject_id=$1) AND action='cancel' ORDER BY changed_at DESC LIMIT 1`,
		orgID).Scan(&metadataRaw); err != nil {
		t.Fatalf("query subscription_history: %v", err)
	}
	var got struct {
		Reason  string `json:"reason"`
		Details string `json:"details"`
	}
	if err := json.Unmarshal(metadataRaw, &got); err != nil {
		t.Fatalf("unmarshal subscription_history metadata: %v", err)
	}
	if got.Reason != "missing_features" {
		t.Errorf("subscription_history metadata: want reason=missing_features, got %q", got.Reason)
	}
	if got.Details != "" {
		t.Errorf("subscription_history metadata: want empty details, got %q", got.Details)
	}
}

// TestIntegration_CancelSubscription_OtherReasonWithDetails covers the
// "other" escape hatch, where details carries the actual free-text reason —
// and also proves details is stored regardless of which reason was picked
// (an owner picking a named reason may still add context; not gated to
// "other" only).
func TestIntegration_CancelSubscription_OtherReasonWithDetails(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_cancelother_user"
		orgID = "00000000-0000-0000-0000-000000000d14"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel",
		`{"reason":"other","details":"Moving to a self-hosted alternative"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("cancel: want 200, got %d: %s", w.Code, w.Body)
	}

	var metadataRaw []byte
	if err := pool.QueryRow(t.Context(),
		`SELECT metadata FROM billing.subscription_history WHERE subscription_id=(SELECT id FROM billing.subscriptions WHERE subject_id=$1) AND action='cancel' ORDER BY changed_at DESC LIMIT 1`,
		orgID).Scan(&metadataRaw); err != nil {
		t.Fatalf("query subscription_history: %v", err)
	}
	var got struct {
		Reason  string `json:"reason"`
		Details string `json:"details"`
	}
	if err := json.Unmarshal(metadataRaw, &got); err != nil {
		t.Fatalf("unmarshal subscription_history metadata: %v", err)
	}
	if got.Reason != "other" || got.Details != "Moving to a self-hosted alternative" {
		t.Errorf("subscription_history metadata: want reason=other with details, got %+v", got)
	}
}

// TestIntegration_CancelSubscription_InvalidReasonRejected mimics a
// malformed/bad-faith client sending a reason outside the closed enum
// through the full stack — must be rejected at binding, before the
// subscription is touched at all.
func TestIntegration_CancelSubscription_InvalidReasonRejected(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_cancelinvalid_user"
		orgID = "00000000-0000-0000-0000-000000000d15"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel", `{"reason":"just_because"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid reason: want 422, got %d: %s", w.Code, w.Body)
	}

	data := getSubscriptionData(t, e, orgID)
	if data["status"] == "cancelled" {
		t.Error("invalid reason must not cancel the subscription")
	}
}

// --- Resume ---

// TestIntegration_ResumeSubscription_Cancelled covers cancel->resume for a
// *trialing* subscription (provisionSubscription always trials a user's
// first-ever organization) — must resume back into "trialing", not "active",
// since the trial window is still open and no invoice was ever created.
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
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel", `{"reason":"too_expensive"}`))

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
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel", `{"reason":"too_expensive"}`))

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
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, billingURL(orgID)+"/plan", `{"plan":"growth","cycle":"monthly","terms_agreed":true}`))
	if w.Code != http.StatusOK {
		t.Fatalf("changePlan: want 200, got %d: %s", w.Code, w.Body)
	}

	data := getSubscriptionData(t, e, orgID)
	if data["plan"] != "growth" {
		t.Errorf("after upgrade: want plan=growth, got %v", data["plan"])
	}
}

// TestIntegration_ChangePlan_SameCycleSwitch_LeavesPeriodEndUnchanged guards
// against a real revenue leak: switching only the billing *cycle* (plan
// unchanged) used to run through the same price-ratio proration as a real
// plan-tier change. Since the yearly price is cheaper per day than monthly
// (the usual prepay discount), re-pricing a subscription's already-banked
// remaining runway at the yearly rate manufactured extra period_end for
// free — no invoice, no payment, just a value-ratio recalculation. A
// subscription with a lot of banked runway (from extends) made this
// dramatic: a growth/monthly sub with ~700 days left jumped by months on a
// same-plan cycle switch alone. Fixed so a pure cycle switch leaves
// period_end untouched; only an actual plan-tier change reprorates it.
func TestIntegration_ChangePlan_SameCycleSwitch_LeavesPeriodEndUnchanged(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_cycleswitch_user"
		orgID = "00000000-0000-0000-0000-000000000d09"
	)
	setupBillingTest(t, pool, orgID)
	subID := seedActiveOrgNoSchedule(t, pool, orgID, user)

	// Simulate a subscription that's banked a lot of runway (e.g. via
	// extends), same as the real-world case this bug was found in.
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET plan = 'growth', cycle = 'monthly', period_end = now() + interval '700 days' WHERE id = $1`,
		subID); err != nil {
		t.Fatalf("seed banked-runway subscription: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})
	before := getSubscriptionData(t, e, orgID)
	beforePeriodEnd, err := time.Parse(time.RFC3339, before["period_end"].(string))
	if err != nil {
		t.Fatalf("parse before period_end: %v", err)
	}

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, billingURL(orgID)+"/plan", `{"plan":"growth","cycle":"yearly","terms_agreed":true}`))
	if w.Code != http.StatusOK {
		t.Fatalf("changePlan (cycle switch only): want 200, got %d: %s", w.Code, w.Body)
	}

	after := getSubscriptionData(t, e, orgID)
	if after["cycle"] != "yearly" {
		t.Errorf("want cycle switched to yearly, got %v", after["cycle"])
	}
	afterPeriodEnd, err := time.Parse(time.RFC3339, after["period_end"].(string))
	if err != nil {
		t.Fatalf("parse after period_end: %v", err)
	}
	if !afterPeriodEnd.Equal(beforePeriodEnd) {
		t.Errorf("a same-plan cycle switch must not move period_end: want unchanged at %v, got %v — a regression to price-ratio proration would push this ~5 months further out", beforePeriodEnd, afterPeriodEnd)
	}
}

// TestIntegration_ChangePlan_RecordsTermsAgreedInHistory posts a real
// request through the full handler->service->DB stack and asserts
// terms_agreed/agreed_at actually land in subscription_history.metadata —
// not a mocked network layer, and not just that the handler accepted the
// shape.
func TestIntegration_ChangePlan_RecordsTermsAgreedInHistory(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_upgradeterms_user"
		orgID = "00000000-0000-0000-0000-000000000d16"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	beforeRequest := time.Now()
	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, billingURL(orgID)+"/plan", `{"plan":"growth","cycle":"monthly","terms_agreed":true}`))
	if w.Code != http.StatusOK {
		t.Fatalf("changePlan: want 200, got %d: %s", w.Code, w.Body)
	}

	var metadataRaw []byte
	if err := pool.QueryRow(t.Context(),
		`SELECT metadata FROM billing.subscription_history WHERE subscription_id=(SELECT id FROM billing.subscriptions WHERE subject_id=$1) AND action='upgrade' ORDER BY changed_at DESC LIMIT 1`,
		orgID).Scan(&metadataRaw); err != nil {
		t.Fatalf("query subscription_history: %v", err)
	}
	var got struct {
		TermsAgreed bool      `json:"terms_agreed"`
		AgreedAt    time.Time `json:"agreed_at"`
	}
	if err := json.Unmarshal(metadataRaw, &got); err != nil {
		t.Fatalf("unmarshal subscription_history metadata: %v", err)
	}
	if !got.TermsAgreed {
		t.Error("subscription_history metadata: want terms_agreed=true")
	}
	if got.AgreedAt.Before(beforeRequest) {
		t.Errorf("subscription_history metadata: agreed_at %v is before the request was made (%v)", got.AgreedAt, beforeRequest)
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
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, billingURL(orgID)+"/plan", `{"plan":"enterprise","cycle":"monthly","terms_agreed":true}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown plan should return 422, got %d: %s", w.Code, w.Body)
	}
}

// TestIntegration_ChangePlan_CancelledRejected is the server-side twin of the
// frontend's canChangePlan gate: a cancelled subscription has no active
// billing to change plans on, so it must resume first. Guards
// changePlanWithMetadata directly, which also covers the downgrade wizard's
// endpoint (it funnels through the same function).
func TestIntegration_ChangePlan_CancelledRejected(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_changeplan_cancelled_user"
		orgID = "00000000-0000-0000-0000-000000000d17"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel", `{"reason":"too_expensive"}`))

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, billingURL(orgID)+"/plan", `{"plan":"growth","cycle":"monthly","terms_agreed":true}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("change plan while cancelled: want 422, got %d: %s", w.Code, w.Body)
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

// TestIntegration_ExpireIfDue_HistoryFailure_RollsBackStatus proves
// expireIfDue's status update and history insert are atomic: if the history
// insert fails, the status change must not have committed either.
func TestIntegration_ExpireIfDue_HistoryFailure_RollsBackStatus(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_expire_txfail_user"
		orgID = "00000000-0000-0000-0000-000000000d18"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	past := time.Now().Add(-time.Second)
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET period_end = $1, trial_end = NULL WHERE subject_type = 'organization' AND subject_id = $2`,
		past, orgID); err != nil {
		t.Fatalf("set past period_end: %v", err)
	}

	subID := getSubscriptionID(pool, orgID)
	statusBefore := getSubscriptionData(t, billing.NewModuleEngine(pool, user, orgID), orgID)["status"].(string)

	forceHistoryInsertFailure(t, pool, subID)

	if err := mod.Worker.HandleSubscriptionCheck(t.Context(), encodeSubscriptionCheckEvent(subID, orgID, past)); err == nil {
		t.Fatal("HandleSubscriptionCheck: want error from failed history insert, got nil")
	}

	data := getSubscriptionData(t, billing.NewModuleEngine(pool, user, orgID), orgID)
	if data["status"] != statusBefore {
		t.Errorf("after failed history insert: want status unchanged (%q), got %v", statusBefore, data["status"])
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

// TestIntegration_Webhook_PaidRenewalRollsOverActivePeriod guards against a
// regression where a routine renewal invoice (HandleSubscriptionAutoInvoice
// creates one 3 days before period_end) paid on time — i.e. while the
// subscription is still "active", the normal case — never advanced
// period_end at all: the reactivation branch only fired for an
// already-"expired" subscription, so expireIfDue would suspend the
// organization on schedule regardless of the on-time payment. Also asserts
// the new period extends from the *old* period_end, not from "now" — the
// invoice is paid up to 3 days early, and rolling from "now" would shave
// those days off the customer's paid term.
func TestIntegration_Webhook_PaidRenewalRollsOverActivePeriod(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_wh_renew_user"
		orgID = "00000000-0000-0000-0000-000000000f06"
		extID = "stripe_sess_f06"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	subID := getSubscriptionID(pool, orgID)
	// New organizations provision on a trial by default (see
	// TestIntegration_Webhook_PaidRenewalConvertsTrialToActive for that
	// path) — force plain "active" here to cover the other branch.
	// Simulate paying 3 days early: period_end is still 3 days out, well
	// before "now" — the bug was rolling from "now" instead of this value.
	oldPeriodEnd := time.Now().Add(3 * 24 * time.Hour).Truncate(time.Second)
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET status = 'active', trial_end = NULL, period_end = $1 WHERE id = $2`,
		oldPeriodEnd, subID); err != nil {
		t.Fatalf("seed period_end: %v", err)
	}

	invID := seedInvoice(pool, subID, "USD", 900) // default kind = 'subscription', matching the real renewal invoice
	seedPaymentLink(pool, invID, extID, "stripe", "USD", 900)

	payload := `{"type":"checkout.session.completed","data":{"object":{"id":"` + extID + `","payment_status":"paid"}}}`
	e := billing.NewWebhookModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if w.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", w.Code, w.Body)
	}

	var status string
	var newPeriodEnd time.Time
	pool.QueryRow(t.Context(), `SELECT status, period_end FROM billing.subscriptions WHERE id = $1`, subID).
		Scan(&status, &newPeriodEnd)
	if status != "active" {
		t.Errorf("after paid renewal: want status=active, got %q", status)
	}
	want := oldPeriodEnd.AddDate(0, 1, 0)
	if !newPeriodEnd.Equal(want) {
		t.Errorf("period_end: want %v (old period_end + 1 month), got %v", want, newPeriodEnd)
	}

	var historyCount int
	pool.QueryRow(t.Context(),
		`SELECT count(*) FROM billing.subscription_history WHERE subscription_id = $1 AND action = 'renew'`, subID,
	).Scan(&historyCount)
	if historyCount != 1 {
		t.Errorf("want 1 'renew' history row, got %d", historyCount)
	}
}

// TestIntegration_Webhook_PaidRenewalConvertsTrialToActive covers the other
// half of the same fix: a trialing subscription's renewal invoice (created
// 3 days before trial_end, same as any other renewal) paid on time must
// convert the subscription to active with trial_end cleared — otherwise
// expireIfDue's trial_end check would still fire at the original (now past)
// trial_end date and undo the renewal on its very next run.
func TestIntegration_Webhook_PaidRenewalConvertsTrialToActive(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_wh_renew_trial_user"
		orgID = "00000000-0000-0000-0000-000000000f07"
		extID = "stripe_sess_f07"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	subID := getSubscriptionID(pool, orgID)
	// New organizations already provision as "trialing" (see setup above) —
	// only need to pin trial_end/period_end to a known, still-future value.
	oldPeriodEnd := time.Now().Add(3 * 24 * time.Hour).Truncate(time.Second)
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET trial_end = $1, period_end = $1 WHERE id = $2`,
		oldPeriodEnd, subID); err != nil {
		t.Fatalf("seed trial_end: %v", err)
	}

	invID := seedInvoice(pool, subID, "USD", 900)
	seedPaymentLink(pool, invID, extID, "stripe", "USD", 900)

	payload := `{"type":"checkout.session.completed","data":{"object":{"id":"` + extID + `","payment_status":"paid"}}}`
	e := billing.NewWebhookModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if w.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", w.Code, w.Body)
	}

	var status string
	var newPeriodEnd time.Time
	var trialEnd *time.Time
	pool.QueryRow(t.Context(), `SELECT status, period_end, trial_end FROM billing.subscriptions WHERE id = $1`, subID).
		Scan(&status, &newPeriodEnd, &trialEnd)
	if status != "active" {
		t.Errorf("after paid trial renewal: want status=active, got %q", status)
	}
	if trialEnd != nil {
		t.Errorf("trial_end must be cleared on conversion, got %v", *trialEnd)
	}
	want := oldPeriodEnd.AddDate(0, 1, 0)
	if !newPeriodEnd.Equal(want) {
		t.Errorf("period_end: want %v (old trial_end + 1 month), got %v", want, newPeriodEnd)
	}
}

// TestIntegration_Webhook_PaidProvisioningInvoice_MonthlyPeriodUnchanged is
// the first regression coverage for a 2nd+ org's own provisioning invoice:
// before this fix, provisionSubscription tagged that invoice kind:
// "subscription" — identical to a genuine renewal invoice — so paying it
// hit handleWebhook's generic renewal case and rolled period_end forward by
// one more cycle on top of what insertSubscription already seeded, granting
// a free extra cycle. Paying the "activation"-kind invoice this fix now
// uses must be a no-op on period_end.
func TestIntegration_Webhook_PaidProvisioningInvoice_MonthlyPeriodUnchanged(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_paidprovision_monthly_user"
		wsID1 = "00000000-0000-0000-0000-000000000f86"
		wsID2 = "00000000-0000-0000-0000-000000000f87"
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

	subID := getSubscriptionID(pool, wsID2)
	var periodStart, periodEndBefore time.Time
	pool.QueryRow(t.Context(), `SELECT period_start, period_end FROM billing.subscriptions WHERE id = $1`, subID).
		Scan(&periodStart, &periodEndBefore)
	// Precondition: a monthly 2nd+ org's period is already 1 month wide at
	// creation — the cycle-aware initial-period seeding only changes
	// behavior for yearly (see the sibling yearly test) — this establishes
	// the baseline the payment-is-a-no-op assertion below is relative to.
	if want := periodStart.AddDate(0, 1, 0); !periodEndBefore.Equal(want) {
		t.Fatalf("precondition: want period_end = period_start + 1 month (%v), got %v", want, periodEndBefore)
	}

	var invID string
	var amountCents int64
	var kind string
	pool.QueryRow(t.Context(),
		`SELECT id, amount_cents, kind FROM billing.invoices WHERE subscription_id = $1 AND status = 'pending'`, subID,
	).Scan(&invID, &amountCents, &kind)
	if kind != "activation" {
		t.Fatalf("want the provisioning invoice kind='activation', got %q", kind)
	}

	const extID = "stripe_sess_paidprovision_monthly"
	seedPaymentLink(pool, invID, extID, "stripe", "USD", amountCents)
	payload := fmt.Sprintf(`{"type":"checkout.session.completed","data":{"object":{"id":%q,"payment_status":"paid"}}}`, extID)
	e := billing.NewWebhookModuleEngine(pool, user, wsID2)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if w.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", w.Code, w.Body)
	}

	var periodEndAfter time.Time
	pool.QueryRow(t.Context(), `SELECT period_end FROM billing.subscriptions WHERE id = $1`, subID).Scan(&periodEndAfter)
	if !periodEndAfter.Equal(periodEndBefore) {
		t.Errorf("want period_end unchanged by paying the activation invoice (still %v), got %v — a regression here means a free extra month was granted", periodEndBefore, periodEndAfter)
	}

	var renewCount int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.subscription_history WHERE subscription_id = $1 AND action = 'renew'`, subID,
	).Scan(&renewCount)
	if renewCount != 0 {
		t.Errorf("want no 'renew' history row for paying an activation invoice, got %d", renewCount)
	}
}

// TestIntegration_Webhook_PaidProvisioningInvoice_YearlyGetsFullYear guards
// two things together: insertSubscription must seed a cycle-aware initial
// period (not hardcode 1 month regardless of cycle), and paying that first
// invoice must not additionally roll the period forward by one more cycle.
// A yearly 2nd+ org must get a 1-year initial period, and paying its
// provisioning invoice must leave that period untouched — not "+1 month at
// creation, then +1 year on payment" (13 months total instead of 12).
func TestIntegration_Webhook_PaidProvisioningInvoice_YearlyGetsFullYear(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_paidprovision_yearly_user"
		wsID1 = "00000000-0000-0000-0000-000000000f88"
		wsID2 = "00000000-0000-0000-0000-000000000f89"
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
	if err := mod.Worker.HandleOrganizationCreated(t.Context(),
		encodeOrganizationCreatedEventWithPlan(wsID2, user, "solo", "yearly")); err != nil {
		t.Fatalf("second provision (yearly): %v", err)
	}

	subID := getSubscriptionID(pool, wsID2)
	var periodStart, periodEndBefore time.Time
	pool.QueryRow(t.Context(), `SELECT period_start, period_end FROM billing.subscriptions WHERE id = $1`, subID).
		Scan(&periodStart, &periodEndBefore)
	// Guards against insertSubscription hardcoding +1 month here regardless
	// of cycle.
	if want := periodStart.AddDate(1, 0, 0); !periodEndBefore.Equal(want) {
		t.Fatalf("want a yearly 2nd+ org's initial period_end = period_start + 1 year (%v), got %v", want, periodEndBefore)
	}

	var invID string
	var amountCents int64
	var kind string
	pool.QueryRow(t.Context(),
		`SELECT id, amount_cents, kind FROM billing.invoices WHERE subscription_id = $1 AND status = 'pending'`, subID,
	).Scan(&invID, &amountCents, &kind)
	if kind != "activation" {
		t.Fatalf("want the provisioning invoice kind='activation', got %q", kind)
	}

	const extID = "stripe_sess_paidprovision_yearly"
	seedPaymentLink(pool, invID, extID, "stripe", "USD", amountCents)
	payload := fmt.Sprintf(`{"type":"checkout.session.completed","data":{"object":{"id":%q,"payment_status":"paid"}}}`, extID)
	e := billing.NewWebhookModuleEngine(pool, user, wsID2)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if w.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", w.Code, w.Body)
	}

	var periodEndAfter time.Time
	pool.QueryRow(t.Context(), `SELECT period_end FROM billing.subscriptions WHERE id = $1`, subID).Scan(&periodEndAfter)
	if !periodEndAfter.Equal(periodEndBefore) {
		t.Errorf("want period_end unchanged at exactly 1 year from period_start (still %v), got %v — a regression here means 13 months were granted instead of 12", periodEndBefore, periodEndAfter)
	}

	var renewCount int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.subscription_history WHERE subscription_id = $1 AND action = 'renew'`, subID,
	).Scan(&renewCount)
	if renewCount != 0 {
		t.Errorf("want no 'renew' history row for paying an activation invoice, got %d", renewCount)
	}
}

// TestIntegration_ExtendSubscription_BlockedByUnpaidProvisioningInvoice
// confirms extendSubscription's guard checks for any pending invoice, not
// only a pending "extension"-kind one (hasPendingInvoiceOfKind) — otherwise
// a freshly-provisioned 2nd+ org — whose own
// first invoice is still unpaid — could still have Extend called on it,
// producing two simultaneously-pending invoices on one subscription.
func TestIntegration_ExtendSubscription_BlockedByUnpaidProvisioningInvoice(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_extend_blocked_unpaid_user"
		wsID1 = "00000000-0000-0000-0000-000000000f90"
		wsID2 = "00000000-0000-0000-0000-000000000f91"
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

	invoiceCountBefore := countInvoices(pool, t, wsID2)
	if invoiceCountBefore != 1 {
		t.Fatalf("precondition: want exactly 1 invoice (the unpaid provisioning invoice), got %d", invoiceCountBefore)
	}

	e := billing.NewModuleEngine(pool, user, wsID2, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"months":1}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("extend on an org with an unpaid provisioning invoice: want 422, got %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Status struct {
			Code string `json:"code"`
		} `json:"status"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status.Code != "EXTENSION_ALREADY_PENDING" {
		t.Errorf("want code=EXTENSION_ALREADY_PENDING, got %q", resp.Status.Code)
	}

	if got := countInvoices(pool, t, wsID2); got != invoiceCountBefore {
		t.Errorf("want no second invoice created by the rejected extend attempt, invoice count before=%d after=%d", invoiceCountBefore, got)
	}
}

// TestIntegration_Webhook_ReactivationRollsBackOnHistoryFailure regression-tests
// that a failure applying the reactivation branch's writes (after the invoice
// is already marked paid, within the same transaction) rolls back the whole
// delivery — same contract as TestIntegration_Webhook_TransientFailureRollsBackMarker,
// but for a failure past the payment-record step, in the reactivation writes
// themselves. Without rollback here, the invoice would end up paid while the
// subscription silently never actually reactivated.
func TestIntegration_Webhook_ReactivationRollsBackOnHistoryFailure(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_wh_reactivate_fail_user"
		orgID = "00000000-0000-0000-0000-000000000f34"
		extID = "stripe_sess_f34"
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

	if _, err := pool.Exec(t.Context(), `
		CREATE OR REPLACE FUNCTION billing.__test_reject_resume_history() RETURNS trigger AS $$
		BEGIN
			IF NEW.action = 'resume' THEN
				RAISE EXCEPTION 'simulated failure applying reactivation';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create trigger function: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		CREATE TRIGGER __test_reject_resume_history BEFORE INSERT ON billing.subscription_history
		FOR EACH ROW EXECUTE FUNCTION billing.__test_reject_resume_history()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS __test_reject_resume_history ON billing.subscription_history`)
		pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS billing.__test_reject_resume_history()`)
	})

	payload := `{"type":"checkout.session.completed","data":{"object":{"id":"` + extID + `","payment_status":"paid"}}}`
	e := billing.NewWebhookModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 (rolled back), got %d: %s", w.Code, w.Body)
	}

	var invStatus, subStatus string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, invID).Scan(&invStatus)
	if invStatus != "pending" {
		t.Errorf("want invoice still pending (rolled back), got %q", invStatus)
	}
	pool.QueryRow(t.Context(), `SELECT status FROM billing.subscriptions WHERE id = $1`, subID).Scan(&subStatus)
	if subStatus != "expired" {
		t.Errorf("want subscription still expired (rolled back, not silently half-applied), got %q", subStatus)
	}
}

// TestIntegration_Webhook_ExtensionRollsBackOnHistoryFailure is
// TestIntegration_Webhook_ReactivationRollsBackOnHistoryFailure's counterpart
// for the extension branch: a failure applying the extension's writes must
// roll back the whole delivery, not leave a paid invoice whose extension
// silently never applied.
func TestIntegration_Webhook_ExtensionRollsBackOnHistoryFailure(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_wh_extend_fail_user"
		wsID1 = "00000000-0000-0000-0000-000000000f61"
		wsID2 = "00000000-0000-0000-0000-000000000f62"
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
	// wsID1 absorbs this user's one-per-user trial; wsID2 provisions active.
	_ = mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user))
	_ = mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID2, user))
	payProvisioningInvoice(pool, wsID2)

	e := billing.NewWebhookModuleEngine(pool, user, wsID2)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"months":1}`))
	var resp struct {
		Data struct {
			ID          string `json:"id"`
			AmountCents int64  `json:"amount_cents"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)

	const extID = "stripe_sess_extend_fail"
	seedPaymentLink(pool, resp.Data.ID, extID, "stripe", "USD", resp.Data.AmountCents)
	beforePeriodEnd := getSubscriptionData(t, e, wsID2)["period_end"]

	if _, err := pool.Exec(t.Context(), `
		CREATE OR REPLACE FUNCTION billing.__test_reject_extend_history() RETURNS trigger AS $$
		BEGIN
			IF NEW.action = 'extend' THEN
				RAISE EXCEPTION 'simulated failure applying extension';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create trigger function: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		CREATE TRIGGER __test_reject_extend_history BEFORE INSERT ON billing.subscription_history
		FOR EACH ROW EXECUTE FUNCTION billing.__test_reject_extend_history()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS __test_reject_extend_history ON billing.subscription_history`)
		pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS billing.__test_reject_extend_history()`)
	})

	payload := fmt.Sprintf(`{"type":"checkout.session.completed","data":{"object":{"id":%q,"payment_status":"paid"}}}`, extID)
	wWeb := httptest.NewRecorder()
	e.ServeHTTP(wWeb, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if wWeb.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 (rolled back), got %d: %s", wWeb.Code, wWeb.Body)
	}

	var invStatus string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, resp.Data.ID).Scan(&invStatus)
	if invStatus != "pending" {
		t.Errorf("want invoice still pending (rolled back), got %q", invStatus)
	}
	dataAfter := getSubscriptionData(t, e, wsID2)
	if dataAfter["period_end"] != beforePeriodEnd {
		t.Error("want period_end unchanged (rolled back, not silently half-applied)")
	}
}

// TestIntegration_Webhook_AddonIncreasePaid_AppliesQuantityAndHistory covers
// the core addon-increase round trip: attaching an increase on a
// non-trialing subscription creates a gating invoice without touching the
// live quantity, and paying that invoice via webhook is what actually
// raises it — the addon_increase case in handleWebhook's switch
// (service_webhook.go).
func TestIntegration_Webhook_AddonIncreasePaid_AppliesQuantityAndHistory(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_wh_addon_incr_user"
		wsID1 = "00000000-0000-0000-0000-000000000f9a"
		wsID2 = "00000000-0000-0000-0000-000000000f9b"
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
	// wsID1 absorbs this user's one-per-user trial; wsID2 provisions active —
	// the addon-increase payment gate only applies to a non-trialing subscription.
	_ = mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user))
	_ = mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID2, user))
	payProvisioningInvoice(pool, wsID2)
	subID := getSubscriptionID(pool, wsID2)

	e := billing.NewWebhookModuleEngine(pool, user, wsID2)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/addons", `{"addon_id":"extra-seat","quantity":5}`))
	if w.Code != http.StatusOK {
		t.Fatalf("attach: want 200, got %d: %s", w.Code, w.Body)
	}
	var attachResp struct {
		Data struct {
			Quantity         int     `json:"quantity"`
			PendingQuantity  *int    `json:"pending_quantity"`
			PendingInvoiceID *string `json:"pending_invoice_id"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&attachResp)
	if attachResp.Data.Quantity != 0 {
		t.Fatalf("want live quantity=0 until paid, got %d", attachResp.Data.Quantity)
	}
	if attachResp.Data.PendingQuantity == nil || *attachResp.Data.PendingQuantity != 5 {
		t.Fatalf("want pending_quantity=5, got %v", attachResp.Data.PendingQuantity)
	}
	if attachResp.Data.PendingInvoiceID == nil {
		t.Fatal("want pending_invoice_id set")
	}
	invoiceID := *attachResp.Data.PendingInvoiceID

	var invKind, invStatus string
	pool.QueryRow(t.Context(), `SELECT kind, status FROM billing.invoices WHERE id = $1`, invoiceID).Scan(&invKind, &invStatus)
	if invKind != "addon_increase" || invStatus != "pending" {
		t.Fatalf("want a pending addon_increase invoice, got kind=%q status=%q", invKind, invStatus)
	}

	const extID = "stripe_sess_addon_incr"
	var amountCents int64
	pool.QueryRow(t.Context(), `SELECT amount_cents FROM billing.invoices WHERE id = $1`, invoiceID).Scan(&amountCents)
	seedPaymentLink(pool, invoiceID, extID, "stripe", "USD", amountCents)

	payload := fmt.Sprintf(`{"type":"checkout.session.completed","data":{"object":{"id":%q,"payment_status":"paid"}}}`, extID)
	wWeb := httptest.NewRecorder()
	e.ServeHTTP(wWeb, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if wWeb.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", wWeb.Code, wWeb.Body)
	}

	var quantity int
	var pendingQuantity *int
	var pendingInvoiceID *string
	pool.QueryRow(t.Context(),
		`SELECT quantity, pending_quantity, pending_invoice_id FROM billing.subscription_addons WHERE subscription_id = $1 AND addon_id = 'extra-seat'`,
		subID,
	).Scan(&quantity, &pendingQuantity, &pendingInvoiceID)
	if quantity != 5 {
		t.Errorf("want live quantity=5 after payment, got %d", quantity)
	}
	if pendingQuantity != nil || pendingInvoiceID != nil {
		t.Errorf("want pending columns cleared after payment, got pending_quantity=%v pending_invoice_id=%v", pendingQuantity, pendingInvoiceID)
	}

	// Two addon_change rows: one written at request time (metadata.pending =
	// true, the increase is only requested, gated on payment) and one at
	// payment-confirmation time (no pending flag, the live quantity actually
	// moved) — see requestAddonIncrease/applyAddonIncreasePayment.
	rows, err := pool.Query(t.Context(),
		`SELECT metadata FROM billing.subscription_history
		 WHERE subscription_id = $1 AND action = 'addon_change' ORDER BY changed_at`,
		subID,
	)
	if err != nil {
		t.Fatalf("query addon_change history: %v", err)
	}
	var metadataRows []map[string]any
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan metadata: %v", err)
		}
		var m map[string]any
		json.Unmarshal(raw, &m)
		metadataRows = append(metadataRows, m)
	}
	rows.Close()
	if len(metadataRows) != 2 {
		t.Fatalf("want exactly 2 addon_change history rows, got %d: %+v", len(metadataRows), metadataRows)
	}
	if metadataRows[0]["pending"] != true {
		t.Errorf("want the first (request-time) row's metadata.pending=true, got %+v", metadataRows[0])
	}
	if metadataRows[0]["from_quantity"] != float64(0) || metadataRows[0]["to_quantity"] != float64(5) {
		t.Errorf("want the request-time row to record 0 -> 5, got %+v", metadataRows[0])
	}
	if metadataRows[1]["pending"] != nil {
		t.Errorf("want the second (payment-confirmed) row's metadata.pending unset, got %+v", metadataRows[1])
	}
	if metadataRows[1]["from_quantity"] != float64(0) || metadataRows[1]["to_quantity"] != float64(5) {
		t.Errorf("want the payment-confirmed row to record 0 -> 5, got %+v", metadataRows[1])
	}
}

// TestIntegration_History_ExposesPhaseCycleAndUndoneRow drives GET /history
// through a real cycle-only plan switch, a scheduled downgrade, and an undo
// of that downgrade — asserting the response carries phase/effective_at/
// from_cycle/to_cycle, and that undoing a schedule writes an explicit
// phase='undone' row mirroring the original 'scheduled' row's from_plan/
// to_plan.
func TestIntegration_History_ExposesPhaseCycleAndUndoneRow(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user       = "integ_billing_history_phase_user"
		trialOrgID = "00000000-0000-0000-0000-000000000f32"
		orgID      = "00000000-0000-0000-0000-000000000f31"
	)
	setupBillingTest(t, pool, trialOrgID)
	setupBillingTest(t, pool, orgID)
	seedBillingOrganization(pool, trialOrgID, user)
	seedBillingOrganization(pool, orgID, user)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(organization.New(pool, messaging.NoopPublisher{}))
	// trialOrgID absorbs this user's one-per-user trial, so orgID (below)
	// provisions active/non-trial — the downgrade-scheduling branch this
	// test exercises only applies to a non-trialing subscription.
	if err := mod.Worker.HandleOrganizationCreated(t.Context(),
		encodeOrganizationCreatedEventWithPlan(trialOrgID, user, "solo", "monthly")); err != nil {
		t.Fatalf("provision trial org: %v", err)
	}
	if err := mod.Worker.HandleOrganizationCreated(t.Context(),
		encodeOrganizationCreatedEventWithPlan(orgID, user, "growth", "monthly")); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})

	// Cycle-only switch (same plan, monthly -> yearly): immediate, action
	// "upgrade" per changePlanWithMetadata's classification (newPlanInfo's
	// sort_order isn't lower than the current plan's).
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, billingURL(orgID)+"/plan",
		`{"plan":"growth","cycle":"yearly","terms_agreed":true}`))
	if w.Code != http.StatusOK {
		t.Fatalf("change cycle: want 200, got %d: %s", w.Code, w.Body)
	}

	// Scheduled downgrade: growth/yearly -> solo/monthly.
	w = httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/downgrade",
		`{"plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("downgrade: want 200, got %d: %s", w.Code, w.Body)
	}

	type historyRow struct {
		Action      string  `json:"action"`
		FromPlan    *string `json:"from_plan"`
		ToPlan      *string `json:"to_plan"`
		Phase       *string `json:"phase"`
		EffectiveAt *string `json:"effective_at"`
		FromCycle   *string `json:"from_cycle"`
		ToCycle     *string `json:"to_cycle"`
	}
	fetchHistory := func() []historyRow {
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(orgID)+"/history", ""))
		if w.Code != http.StatusOK {
			t.Fatalf("GET history: want 200, got %d: %s", w.Code, w.Body)
		}
		var resp struct {
			Data []historyRow `json:"data"`
		}
		json.NewDecoder(w.Body).Decode(&resp)
		return resp.Data
	}

	history := fetchHistory()
	var cycleRow, scheduledRow *historyRow
	for i := range history {
		h := &history[i]
		switch {
		case h.FromCycle != nil && *h.FromCycle == "monthly" && h.ToCycle != nil && *h.ToCycle == "yearly":
			cycleRow = h
		case h.Phase != nil && *h.Phase == "scheduled":
			scheduledRow = h
		}
	}
	if cycleRow == nil {
		t.Fatalf("want a history row exposing from_cycle=monthly/to_cycle=yearly, got %+v", history)
	}
	if cycleRow.Phase != nil {
		t.Errorf("want the immediate cycle-switch row to have phase=nil, got %v", *cycleRow.Phase)
	}
	if scheduledRow == nil {
		t.Fatalf("want a history row exposing phase=scheduled for the downgrade, got %+v", history)
	}
	if scheduledRow.FromPlan == nil || *scheduledRow.FromPlan != "growth" ||
		scheduledRow.ToPlan == nil || *scheduledRow.ToPlan != "solo" {
		t.Errorf("want the scheduled row's from_plan/to_plan = growth/solo, got %+v", scheduledRow)
	}
	if scheduledRow.FromCycle == nil || *scheduledRow.FromCycle != "yearly" ||
		scheduledRow.ToCycle == nil || *scheduledRow.ToCycle != "monthly" {
		t.Errorf("want the scheduled row's from_cycle/to_cycle = yearly/monthly, got %+v", scheduledRow)
	}
	if scheduledRow.EffectiveAt == nil {
		t.Error("want the scheduled row's effective_at set")
	}

	// Undo it — expect a new phase='undone' row mirroring the scheduled row,
	// not just the scheduled row silently never getting an 'applied' match.
	w = httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/downgrade/undo", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("undo downgrade: want 200, got %d: %s", w.Code, w.Body)
	}

	history = fetchHistory()
	var undoneRow *historyRow
	for i := range history {
		if history[i].Phase != nil && *history[i].Phase == "undone" {
			undoneRow = &history[i]
			break
		}
	}
	if undoneRow == nil {
		t.Fatalf("want a phase='undone' history row after undo, got %+v", history)
	}
	if undoneRow.FromPlan == nil || *undoneRow.FromPlan != "growth" ||
		undoneRow.ToPlan == nil || *undoneRow.ToPlan != "solo" {
		t.Errorf("want the undone row to mirror the scheduled row's from_plan/to_plan = growth/solo, got %+v", undoneRow)
	}
}

// TestIntegration_Webhook_AddonIncreaseFailed_LeavesPendingRetryable covers
// the edge case where a failed payment on an addon-increase invoice must
// not touch pending_quantity/pending_invoice_id — the customer can retry
// payment via the existing regenerate-link flow without re-requesting the
// increase. No addon_increase-specific failure code exists (the existing
// outcome.failed path fires regardless of kind); this proves that
// fall-through is correct for this kind specifically, then confirms a
// second, successful delivery still applies the increase normally.
func TestIntegration_Webhook_AddonIncreaseFailed_LeavesPendingRetryable(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_wh_addon_incr_fail_user"
		wsID1 = "00000000-0000-0000-0000-000000000f9c"
		wsID2 = "00000000-0000-0000-0000-000000000f9d"
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
	_ = mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user))
	_ = mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID2, user))
	payProvisioningInvoice(pool, wsID2)
	subID := getSubscriptionID(pool, wsID2)

	e := billing.NewWebhookModuleEngine(pool, user, wsID2)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/addons", `{"addon_id":"extra-seat","quantity":5}`))
	var attachResp struct {
		Data struct {
			PendingInvoiceID *string `json:"pending_invoice_id"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&attachResp)
	invoiceID := *attachResp.Data.PendingInvoiceID

	const extID = "xendit_inv_addon_incr_fail"
	var amountCents int64
	pool.QueryRow(t.Context(), `SELECT amount_cents FROM billing.invoices WHERE id = $1`, invoiceID).Scan(&amountCents)
	seedPaymentLink(pool, invoiceID, extID, "xendit", "USD", amountCents)

	wFail := httptest.NewRecorder()
	e.ServeHTTP(wFail, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/xendit", `{"id":"`+extID+`","status":"FAILED"}`))
	if wFail.Code != http.StatusOK {
		t.Fatalf("FAILED webhook: want 200, got %d: %s", wFail.Code, wFail.Body)
	}

	var quantity int
	var pendingQuantity *int
	var pendingInvoiceID *string
	var invStatus string
	pool.QueryRow(t.Context(),
		`SELECT quantity, pending_quantity, pending_invoice_id FROM billing.subscription_addons WHERE subscription_id = $1 AND addon_id = 'extra-seat'`,
		subID,
	).Scan(&quantity, &pendingQuantity, &pendingInvoiceID)
	pool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, invoiceID).Scan(&invStatus)
	if quantity != 0 {
		t.Errorf("want live quantity still 0 after a failed payment, got %d", quantity)
	}
	if pendingQuantity == nil || *pendingQuantity != 5 || pendingInvoiceID == nil || *pendingInvoiceID != invoiceID {
		t.Errorf("want pending state untouched and retryable after a failed payment, got pending_quantity=%v pending_invoice_id=%v", pendingQuantity, pendingInvoiceID)
	}
	if invStatus != "pending" {
		t.Errorf("want the invoice still pending (payable again), got status=%q", invStatus)
	}

	// Retry: a second, successful delivery for the same invoice still applies normally.
	wPaid := httptest.NewRecorder()
	e.ServeHTTP(wPaid, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/xendit", `{"id":"`+extID+`","status":"PAID"}`))
	if wPaid.Code != http.StatusOK {
		t.Fatalf("PAID retry webhook: want 200, got %d: %s", wPaid.Code, wPaid.Body)
	}
	pool.QueryRow(t.Context(),
		`SELECT quantity, pending_quantity, pending_invoice_id FROM billing.subscription_addons WHERE subscription_id = $1 AND addon_id = 'extra-seat'`,
		subID,
	).Scan(&quantity, &pendingQuantity, &pendingInvoiceID)
	if quantity != 5 || pendingQuantity != nil || pendingInvoiceID != nil {
		t.Errorf("want the retried payment to apply the increase, got quantity=%d pending_quantity=%v pending_invoice_id=%v", quantity, pendingQuantity, pendingInvoiceID)
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

	expectedEnd, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, expectedEnd, isTrial)
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

	expectedEnd, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, expectedEnd, isTrial)
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

// TestIntegration_HandleSubscriptionAutoInvoice_LineItemFailure_RollsBackInvoice
// is a regression test: HandleSubscriptionAutoInvoice used to insert the
// invoice, its plan line item, and its charges directly
// against the pool with no transaction, unlike every other invoice-creation
// site in this module. A failure between insertInvoice and
// insertPlanLineItem/applyInvoiceCharges left a permanently orphaned invoice
// row — correct amount_cents, but a wrong/missing line-item breakdown — that
// redelivery never repaired (findPendingInvoiceBySubscription only checks
// whether a pending invoice exists, not whether its line items are
// complete). Simulated the same way as
// TestIntegration_Webhook_ReactivationRollsBackOnHistoryFailure/
// ExtensionRollsBackOnHistoryFailure: a trigger that rejects the write the
// failure would have hit.
func TestIntegration_HandleSubscriptionAutoInvoice_LineItemFailure_RollsBackInvoice(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_autoinv_lineitem_fail_user"
		orgID = "00000000-0000-0000-0000-000000000a05"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)

	if _, err := pool.Exec(t.Context(), `
		CREATE OR REPLACE FUNCTION billing.__test_reject_line_item() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'simulated failure inserting invoice line item';
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create trigger function: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		CREATE TRIGGER __test_reject_line_item BEFORE INSERT ON billing.invoice_line_items
		FOR EACH ROW EXECUTE FUNCTION billing.__test_reject_line_item()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS __test_reject_line_item ON billing.invoice_line_items`)
		pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS billing.__test_reject_line_item()`)
	})

	expectedEnd, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, expectedEnd, isTrial)
	if err := mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body); err == nil {
		t.Fatal("HandleSubscriptionAutoInvoice: want an error from the simulated line-item failure, got nil")
	}

	var invoiceCount int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.invoices WHERE subscription_id = $1`, subID,
	).Scan(&invoiceCount)
	if invoiceCount != 0 {
		t.Errorf("want zero invoice rows (rolled back, not an orphaned invoice with a missing line-item breakdown), got %d", invoiceCount)
	}
}

// TestIntegration_HandleSubscriptionAutoInvoice_StaleExpectedEnd_SkipsInvoice
// regression-tests the guard for delayed auto-invoice checks.
// A delayed auto-invoice check scheduled against a period_end that has since
// changed (e.g. the subscription was extended after this message was queued)
// must not generate a premature renewal invoice for the stale date.
func TestIntegration_HandleSubscriptionAutoInvoice_StaleExpectedEnd_SkipsInvoice(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_autoinv_stale_user"
		orgID = "00000000-0000-0000-0000-000000000a12"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)

	// A stale ExpectedEnd — doesn't match the subscription's real current end
	// (simulates a message scheduled before the period was later changed).
	body := encodeSubscriptionCheckEvent(subID, orgID, time.Now().Add(365*24*time.Hour))
	if err := mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body); err != nil {
		t.Errorf("HandleSubscriptionAutoInvoice stale check: want nil error, got %v", err)
	}

	var invoiceCount int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.invoices WHERE subscription_id = $1`, subID,
	).Scan(&invoiceCount)
	if invoiceCount != 0 {
		t.Errorf("want no invoice generated for a stale ExpectedEnd, got %d", invoiceCount)
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

	expectedEnd, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, expectedEnd, isTrial)
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
	expectedEnd, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, expectedEnd, isTrial)

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

// --- scheduled-cancellation reconciliation ---

// TestIntegration_HandleSubscriptionAutoInvoice_NoScheduledCancellation_InvoicesAsBefore
// regression-tests that wiring an OrganizationSuspender (unused by every
// other test in this file) doesn't change behavior for the common case:
// with nothing scheduled to cancel, SuspendOrganization must never be
// called and the existing invoice/payment-link flow runs exactly as before.
func TestIntegration_HandleSubscriptionAutoInvoice_NoScheduledCancellation_InvoicesAsBefore(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_autoinv_nosched_user"
		orgID = "00000000-0000-0000-0000-000000000f96"
	)
	setupBillingTest(t, pool, orgID)

	suspender := &stubOrgSuspender{}
	mod := billing.New(pool, messaging.NoopPublisher{}, billing.ProviderConfig{}, stubRefReader{}, suspender)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	expectedEnd, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, expectedEnd, isTrial)

	// createPaymentLink still fails with an empty ProviderConfig — that's
	// fine, it only proves the invoice path was reached, same as every
	// other test in this file using NewModuleForTest.
	_ = mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body)

	if len(suspender.calls) != 0 {
		t.Errorf("want SuspendOrganization never called with nothing scheduled, got %d calls", len(suspender.calls))
	}
	var invoiceCount int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.invoices WHERE subscription_id = $1`, subID).Scan(&invoiceCount)
	if invoiceCount != 1 {
		t.Errorf("want 1 invoice with nothing scheduled to cancel, got %d", invoiceCount)
	}
}

// TestIntegration_HandleSubscriptionAutoInvoice_ScheduledCancellation_SuspendsAndCancels
// covers the cancellation branch's full success path: SuspendOrganization is
// called with the subscription's SubjectID, status becomes cancelled, one
// applied history row is written, SubscriptionCancelled is published, and no
// invoice/line item/payment link is created.
func TestIntegration_HandleSubscriptionAutoInvoice_ScheduledCancellation_SuspendsAndCancels(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_autoinv_cancel_user"
		orgID = "00000000-0000-0000-0000-000000000f97"
	)
	setupBillingTest(t, pool, orgID)

	suspender := &stubOrgSuspender{}
	pub := &capturingPublisher{}
	mod := billing.New(pool, pub, billing.ProviderConfig{}, stubRefReader{}, suspender)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET status = 'active', scheduled_cancel_at = now() WHERE id = $1`, subID); err != nil {
		t.Fatalf("seed scheduled cancellation: %v", err)
	}
	expectedEnd, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, expectedEnd, isTrial)

	if err := mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body); err != nil {
		t.Fatalf("want nil error applying a scheduled cancellation, got %v", err)
	}

	if len(suspender.calls) != 1 {
		t.Fatalf("want SuspendOrganization called once, got %d", len(suspender.calls))
	}
	if suspender.calls[0].orgID != orgID || suspender.calls[0].reason != "subscription cancelled" {
		t.Errorf("want SuspendOrganization(%q, %q), got (%q, %q)", orgID, "subscription cancelled", suspender.calls[0].orgID, suspender.calls[0].reason)
	}

	var status string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.subscriptions WHERE id = $1`, subID).Scan(&status)
	if status != "cancelled" {
		t.Errorf("want status=cancelled, got %q", status)
	}

	var historyCount int
	var phase string
	var effectiveAt *time.Time
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*), MAX(phase), MAX(effective_at) FROM billing.subscription_history WHERE subscription_id = $1 AND action = 'cancel'`,
		subID).Scan(&historyCount, &phase, &effectiveAt)
	if historyCount != 1 || phase != "applied" {
		t.Errorf("want 1 cancel history row with phase=applied, got count=%d phase=%q", historyCount, phase)
	}
	if effectiveAt == nil {
		t.Error("want effective_at set on the applied cancel history row")
	}

	var invoiceCount int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.invoices WHERE subscription_id = $1`, subID).Scan(&invoiceCount)
	if invoiceCount != 0 {
		t.Errorf("want no invoice created for a cancelled subscription, got %d", invoiceCount)
	}

	found := slices.ContainsFunc(pub.published, func(e capturedEvent) bool {
		return e.routingKey == events.RoutingKeySubscriptionCancelled
	})
	if !found {
		t.Errorf("want a %s event published, got routing keys %v", events.RoutingKeySubscriptionCancelled, pub.published)
	}
}

// TestIntegration_HandleSubscriptionAutoInvoice_SuspendFails_RollsBackAndRetrySucceeds
// covers the redelivery-safety property a message-queue consumer needs: if
// SuspendOrganization fails, nothing must be left half-applied (status and
// scheduled_cancel_at unchanged, no history row, no event), and a later
// redelivery with SuspendOrganization now succeeding completes correctly.
func TestIntegration_HandleSubscriptionAutoInvoice_SuspendFails_RollsBackAndRetrySucceeds(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_autoinv_cancel_retry_user"
		orgID = "00000000-0000-0000-0000-000000000f98"
	)
	setupBillingTest(t, pool, orgID)

	suspender := &stubOrgSuspender{failCount: 1}
	pub := &capturingPublisher{}
	mod := billing.New(pool, pub, billing.ProviderConfig{}, stubRefReader{}, suspender)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET status = 'active', scheduled_cancel_at = now() WHERE id = $1`, subID); err != nil {
		t.Fatalf("seed scheduled cancellation: %v", err)
	}
	expectedEnd, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, expectedEnd, isTrial)

	// First delivery: SuspendOrganization fails — the whole reconciliation
	// must roll back, touching nothing.
	if err := mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body); err == nil {
		t.Fatal("first delivery: want an error from the failed SuspendOrganization call")
	}

	var status string
	var scheduledCancelAt *time.Time
	pool.QueryRow(t.Context(), `SELECT status, scheduled_cancel_at FROM billing.subscriptions WHERE id = $1`, subID).Scan(&status, &scheduledCancelAt)
	if status != "active" || scheduledCancelAt == nil {
		t.Fatalf("after a failed suspend: want status=active with scheduled_cancel_at still set, got status=%q scheduled_cancel_at=%v", status, scheduledCancelAt)
	}
	var historyCountAfterFailure int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.subscription_history WHERE subscription_id = $1 AND action = 'cancel'`,
		subID).Scan(&historyCountAfterFailure)
	if historyCountAfterFailure != 0 {
		t.Errorf("after a failed suspend: want no cancel history row, got %d", historyCountAfterFailure)
	}
	// pub also carries HandleOrganizationCreated's own provisioning events
	// (trial-started, the delayed subscription-check/-remind/-auto-invoice
	// scheduling) from setup above — check specifically for the one this
	// failed attempt must not have queued, not the publisher's full history.
	if slices.ContainsFunc(pub.published, func(e capturedEvent) bool {
		return e.routingKey == events.RoutingKeySubscriptionCancelled
	}) {
		t.Error("after a failed suspend: want no SubscriptionCancelled event published")
	}

	// Redelivery: SuspendOrganization now succeeds (failCount exhausted).
	if err := mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body); err != nil {
		t.Fatalf("redelivery: want nil error once SuspendOrganization succeeds, got %v", err)
	}
	pool.QueryRow(t.Context(), `SELECT status FROM billing.subscriptions WHERE id = $1`, subID).Scan(&status)
	if status != "cancelled" {
		t.Errorf("after successful redelivery: want status=cancelled, got %q", status)
	}
	if len(suspender.calls) != 2 {
		t.Errorf("want SuspendOrganization called twice (failed once, succeeded once), got %d", len(suspender.calls))
	}
}

// TestIntegration_HandleSubscriptionAutoInvoice_RedeliveryAfterAppliedCancellation_IsNoOp
// covers event redelivery after a cancellation has already fully applied:
// the existing status guard (not active/trialing) must catch it before
// reaching the cancellation branch at all, so SuspendOrganization is not
// called a second time for the same, already-cancelled subscription.
func TestIntegration_HandleSubscriptionAutoInvoice_RedeliveryAfterAppliedCancellation_IsNoOp(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_autoinv_cancel_redeliver_user"
		orgID = "00000000-0000-0000-0000-000000000f99"
	)
	setupBillingTest(t, pool, orgID)

	suspender := &stubOrgSuspender{}
	mod := billing.New(pool, messaging.NoopPublisher{}, billing.ProviderConfig{}, stubRefReader{}, suspender)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	expectedEnd, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, expectedEnd, isTrial)

	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET status = 'cancelled', scheduled_cancel_at = now() WHERE id = $1`, subID); err != nil {
		t.Fatalf("seed already-cancelled subscription: %v", err)
	}

	if err := mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body); err != nil {
		t.Errorf("want nil error redelivering against an already-cancelled subscription, got %v", err)
	}
	if len(suspender.calls) != 0 {
		t.Errorf("want SuspendOrganization not called again for an already-cancelled subscription, got %d calls", len(suspender.calls))
	}
}

// --- scheduled-overage reconciliation (worker) ---

// TestIntegration_HandleSubscriptionAutoInvoice_NothingScheduled_OverageUntouched
// regression-tests that wiring an OrganizationCommander (unused by every
// prior test in this file besides the downgrade/preview HTTP flows) doesn't
// change behavior for the common case: with nothing scheduled, the overage
// step must never call ResolveDowngradeOverage and the existing invoice flow
// runs exactly as before.
func TestIntegration_HandleSubscriptionAutoInvoice_NothingScheduled_OverageUntouched(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_autoinv_overage_none_user"
		orgID = "00000000-0000-0000-0000-00000000e001"
	)
	setupBillingTest(t, pool, orgID)

	commander := &spyOrgCommander{}
	mod := billing.New(pool, messaging.NoopPublisher{}, billing.ProviderConfig{}, stubRefReader{}, nil)
	mod.SetOrganizationCommander(commander)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET status = 'active' WHERE id = $1`, subID); err != nil {
		t.Fatalf("force active: %v", err)
	}
	expectedEnd, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, expectedEnd, isTrial)

	// createPaymentLink still fails with an empty ProviderConfig — that only
	// proves the invoice path was reached, same as every other test in this
	// file using NewModuleForTest.
	_ = mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body)

	if commander.calls != 0 {
		t.Errorf("want ResolveDowngradeOverage never called with nothing scheduled, got %d calls", commander.calls)
	}
	var invoiceCount int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.invoices WHERE subscription_id = $1`, subID).Scan(&invoiceCount)
	if invoiceCount != 1 {
		t.Errorf("want 1 invoice with nothing scheduled, got %d", invoiceCount)
	}
}

// TestIntegration_HandleSubscriptionAutoInvoice_ScheduledPlanDowngrade_WithinLimits_AppliesWithoutOverageCall
// covers a scheduled plan downgrade whose future limits current usage stays
// within: ResolveDowngradeOverage must not be called, and the plan/cycle
// swap plus its applied history row still happen.
func TestIntegration_HandleSubscriptionAutoInvoice_ScheduledPlanDowngrade_WithinLimits_AppliesWithoutOverageCall(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_autoinv_overage_ok_user"
		orgID = "00000000-0000-0000-0000-00000000e002"
	)
	setupBillingTest(t, pool, orgID)

	commander := &spyOrgCommander{}
	mod := billing.New(pool, messaging.NoopPublisher{}, billing.ProviderConfig{}, stubRefReader{}, nil)
	mod.SetOrganizationCommander(commander)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET status = 'active', plan = 'growth' WHERE id = $1`, subID); err != nil {
		t.Fatalf("force active growth: %v", err)
	}
	seedScheduledPlanDowngrade(t, pool, subID)
	seedMemberUsage(t, pool, orgID, 1) // solo's limit is 1 — exactly at, not over

	expectedEnd, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, expectedEnd, isTrial)
	_ = mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body)

	if commander.calls != 0 {
		t.Errorf("want ResolveDowngradeOverage not called when usage is within the new plan's limit, got %d calls", commander.calls)
	}
	var plan string
	var scheduledPlan *string
	pool.QueryRow(t.Context(), `SELECT plan, scheduled_plan FROM billing.subscriptions WHERE id = $1`, subID).Scan(&plan, &scheduledPlan)
	if plan != "solo" || scheduledPlan != nil {
		t.Errorf("want plan=solo and scheduled_plan cleared, got plan=%q scheduled_plan=%v", plan, scheduledPlan)
	}
	var historyCount int
	var phase string
	var effectiveAt *time.Time
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*), MAX(phase), MAX(effective_at) FROM billing.subscription_history WHERE subscription_id = $1 AND action = 'downgrade'`,
		subID).Scan(&historyCount, &phase, &effectiveAt)
	if historyCount != 1 || phase != "applied" {
		t.Errorf("want 1 downgrade history row with phase=applied, got count=%d phase=%q", historyCount, phase)
	}
	if effectiveAt == nil {
		t.Error("want effective_at set on the applied downgrade history row")
	}
}

// TestIntegration_HandleSubscriptionAutoInvoice_ScheduledPlanDowngrade_OverLimit_CallsOverageBeforeApplying
// covers a scheduled plan downgrade whose future limit current usage
// exceeds: ResolveDowngradeOverage must be called before the plan swap
// commits.
func TestIntegration_HandleSubscriptionAutoInvoice_ScheduledPlanDowngrade_OverLimit_CallsOverageBeforeApplying(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_autoinv_overage_plan_user"
		orgID = "00000000-0000-0000-0000-00000000e003"
	)
	setupBillingTest(t, pool, orgID)

	commander := &spyOrgCommander{}
	mod := billing.New(pool, messaging.NoopPublisher{}, billing.ProviderConfig{}, stubRefReader{}, nil)
	mod.SetOrganizationCommander(commander)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET status = 'active', plan = 'growth' WHERE id = $1`, subID); err != nil {
		t.Fatalf("force active growth: %v", err)
	}
	seedScheduledPlanDowngrade(t, pool, subID)
	seedMemberUsage(t, pool, orgID, 2) // solo's limit is 1 — over

	expectedEnd, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, expectedEnd, isTrial)
	_ = mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body)

	if commander.calls != 1 {
		t.Errorf("want ResolveDowngradeOverage called once when usage exceeds the new plan's limit, got %d calls", commander.calls)
	}
	var plan string
	pool.QueryRow(t.Context(), `SELECT plan FROM billing.subscriptions WHERE id = $1`, subID).Scan(&plan)
	if plan != "solo" {
		t.Errorf("want plan swap to commit once overage resolution succeeds, got plan=%q", plan)
	}
}

// TestIntegration_HandleSubscriptionAutoInvoice_ScheduledAddonDecrease_OverLimit_AppliesQuantity
// covers a scheduled addon quantity decrease (no plan change) whose
// resulting limit current usage exceeds: ResolveDowngradeOverage is called
// once, and only on success does the addon's live quantity update.
func TestIntegration_HandleSubscriptionAutoInvoice_ScheduledAddonDecrease_OverLimit_AppliesQuantity(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_autoinv_overage_addon_user"
		orgID = "00000000-0000-0000-0000-00000000e004"
	)
	setupBillingTest(t, pool, orgID)

	commander := &spyOrgCommander{}
	mod := billing.New(pool, messaging.NoopPublisher{}, billing.ProviderConfig{}, stubRefReader{}, nil)
	mod.SetOrganizationCommander(commander)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET status = 'active' WHERE id = $1`, subID); err != nil {
		t.Fatalf("force active: %v", err)
	}
	// solo (limit 1) + extra-seat x2 live = 3 today; scheduled down to x1 -> future limit 2.
	seedAddonWithScheduledQuantity(t, pool, subID, "extra-seat", 2, 1)
	seedMemberUsage(t, pool, orgID, 3) // over the future limit of 2

	expectedEnd, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, expectedEnd, isTrial)
	_ = mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body)

	if commander.calls != 1 {
		t.Errorf("want ResolveDowngradeOverage called once for a scheduled addon decrease over the future limit, got %d calls", commander.calls)
	}
	var quantity int
	var scheduledQuantity *int
	pool.QueryRow(t.Context(),
		`SELECT quantity, scheduled_quantity FROM billing.subscription_addons WHERE subscription_id = $1 AND addon_id = 'extra-seat'`,
		subID).Scan(&quantity, &scheduledQuantity)
	if quantity != 1 || scheduledQuantity != nil {
		t.Errorf("want addon quantity=1 and scheduled_quantity cleared, got quantity=%d scheduled_quantity=%v", quantity, scheduledQuantity)
	}
}

// TestIntegration_HandleSubscriptionAutoInvoice_CombinedPlanAndAddon_ExactlyOneOverageCall
// covers the specific compounding scenario a scheduled plan downgrade *and*
// scheduled addon decreases together create: overage must resolve exactly
// once against the fully combined future state, never two separate calls —
// the case most likely to regress silently into two calls if this block is
// later "simplified" into two independent checks.
func TestIntegration_HandleSubscriptionAutoInvoice_CombinedPlanAndAddon_ExactlyOneOverageCall(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_autoinv_overage_combo_user"
		orgID = "00000000-0000-0000-0000-00000000e005"
	)
	setupBillingTest(t, pool, orgID)

	commander := &spyOrgCommander{}
	mod := billing.New(pool, messaging.NoopPublisher{}, billing.ProviderConfig{}, stubRefReader{}, nil)
	mod.SetOrganizationCommander(commander)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET status = 'active', plan = 'growth' WHERE id = $1`, subID); err != nil {
		t.Fatalf("force active growth: %v", err)
	}
	seedScheduledPlanDowngrade(t, pool, subID)
	// solo (limit 1) + extra-seat scheduled down to x1 -> combined future limit 2.
	seedAddonWithScheduledQuantity(t, pool, subID, "extra-seat", 2, 1)
	seedMemberUsage(t, pool, orgID, 3) // over the combined future limit of 2

	expectedEnd, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, expectedEnd, isTrial)
	_ = mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body)

	if commander.calls != 1 {
		t.Errorf("want exactly 1 combined ResolveDowngradeOverage call for a plan downgrade and an addon decrease scheduled together, got %d", commander.calls)
	}
	var plan string
	var quantity int
	pool.QueryRow(t.Context(), `SELECT plan FROM billing.subscriptions WHERE id = $1`, subID).Scan(&plan)
	pool.QueryRow(t.Context(),
		`SELECT quantity FROM billing.subscription_addons WHERE subscription_id = $1 AND addon_id = 'extra-seat'`,
		subID).Scan(&quantity)
	if plan != "solo" || quantity != 1 {
		t.Errorf("want both the plan swap and the addon quantity change applied together, got plan=%q addon_quantity=%d", plan, quantity)
	}
}

// TestIntegration_HandleSubscriptionAutoInvoice_OverageResolutionFails_RollsBackAndRetrySucceeds
// covers the same redelivery-safety property the cancellation branch above
// requires, here for the overage branch: a failed
// ResolveDowngradeOverage call must leave scheduled_plan/the addon's
// scheduled_quantity fully intact (nothing half-applied), and a later
// redelivery with the call now succeeding must complete correctly.
func TestIntegration_HandleSubscriptionAutoInvoice_OverageResolutionFails_RollsBackAndRetrySucceeds(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_autoinv_overage_retry_user"
		orgID = "00000000-0000-0000-0000-00000000e006"
	)
	setupBillingTest(t, pool, orgID)

	commander := &spyOrgCommander{failCount: 1}
	mod := billing.New(pool, messaging.NoopPublisher{}, billing.ProviderConfig{}, stubRefReader{}, nil)
	mod.SetOrganizationCommander(commander)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET status = 'active', plan = 'growth' WHERE id = $1`, subID); err != nil {
		t.Fatalf("force active growth: %v", err)
	}
	seedScheduledPlanDowngrade(t, pool, subID)
	seedMemberUsage(t, pool, orgID, 2) // over solo's limit of 1

	expectedEnd, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, expectedEnd, isTrial)

	// First delivery: ResolveDowngradeOverage fails — nothing must apply.
	if err := mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body); err == nil {
		t.Fatal("first delivery: want an error from the failed overage resolution")
	}
	var plan string
	var scheduledPlan *string
	pool.QueryRow(t.Context(), `SELECT plan, scheduled_plan FROM billing.subscriptions WHERE id = $1`, subID).Scan(&plan, &scheduledPlan)
	if plan != "growth" || scheduledPlan == nil || *scheduledPlan != "solo" {
		t.Fatalf("after a failed overage resolution: want plan=growth with scheduled_plan still solo, got plan=%q scheduled_plan=%v", plan, scheduledPlan)
	}
	var historyCount int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.subscription_history WHERE subscription_id = $1 AND action = 'downgrade'`,
		subID).Scan(&historyCount)
	if historyCount != 0 {
		t.Errorf("after a failed overage resolution: want no downgrade history row, got %d", historyCount)
	}

	// Redelivery: ResolveDowngradeOverage now succeeds (failCount exhausted).
	// The overage step's own transaction commits independently of what
	// happens next — createPaymentLink still fails with an empty
	// ProviderConfig, same as every other test in this file using
	// billing.New/NewModuleForTest, so that error is expected and ignored.
	_ = mod.Worker.HandleSubscriptionAutoInvoice(t.Context(), body)
	pool.QueryRow(t.Context(), `SELECT plan FROM billing.subscriptions WHERE id = $1`, subID).Scan(&plan)
	if plan != "solo" {
		t.Errorf("after successful redelivery: want plan=solo, got %q", plan)
	}
	if commander.calls != 2 {
		t.Errorf("want ResolveDowngradeOverage called twice (failed once, succeeded once), got %d", commander.calls)
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

// TestIntegration_Webhook_ConcurrentFailedDeliveries_AppliesOnce covers two
// distinct Stripe events (different event IDs, so not deduplicated by
// processWebhook's eventID-based idempotency marker) both resolving to
// "failed" for the same payment link, delivered concurrently — must apply
// failure effects exactly once. Before the fix, handleWebhook's
// subscription lock only covered the paid branch — the failed/expired
// branch had no lock, so both deliveries could read the same pre-update
// link status and both apply failure effects, doubling the InvoiceFailed
// publish and the two delayed dunning reminders.
func TestIntegration_Webhook_ConcurrentFailedDeliveries_AppliesOnce(t *testing.T) {
	pool := testPoolBilling(t)
	// Unlike this file's other fixtures, this test's own pass condition
	// depends on its two event IDs never having been seen before (that's
	// what proves they aren't deduplicated) — a fixed literal would collide
	// with billing.webhook_events rows a prior local run already committed,
	// so every ID here is generated fresh per run instead.
	runSuffix := uuid.NewString()[:8]
	user := "integ_billing_wh_race_user_" + runSuffix
	orgID := uuid.NewString()
	extID := "stripe_sess_race_" + runSuffix
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	subID := getSubscriptionID(pool, orgID)
	invID := seedInvoice(pool, subID, "USD", 900)
	seedPaymentLink(pool, invID, extID, "stripe", "USD", 900)

	pub := &syncCapturingPublisher{}
	e := billing.NewWebhookModuleEngineWithPublisher(pool, user, orgID, pub)

	payloadFor := func(eventID string) string {
		return `{"id":"` + eventID + `","type":"checkout.session.expired","data":{"object":{"id":"` + extID + `"}}}`
	}

	var wg sync.WaitGroup
	codes := make([]int, 2)
	// Prefixed with extID so setupBillingTest's cleanup (which matches
	// webhook_events by "event_id LIKE external_id || '%'") reaps these too.
	for i, evtID := range []string{extID + "_evt_a", extID + "_evt_b"} {
		wg.Go(func() {
			w := httptest.NewRecorder()
			e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payloadFor(evtID)))
			codes[i] = w.Code
		})
	}
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("delivery %d: want 200, got %d", i, code)
		}
	}

	published := pub.snapshot()
	var failedCount, remindCount, finalCount int
	for _, ev := range published {
		switch ev.routingKey {
		case events.RoutingKeyInvoiceFailed:
			failedCount++
		case events.DelayRoutingKeySubscriptionPaymentRemind:
			remindCount++
		case events.DelayRoutingKeySubscriptionPaymentFinal:
			finalCount++
		}
	}
	if failedCount != 1 {
		t.Errorf("want exactly 1 InvoiceFailed publish from 2 concurrent distinct-eventID failed deliveries, got %d (published: %+v)", failedCount, published)
	}
	if remindCount != 1 || finalCount != 1 {
		t.Errorf("want exactly 1 payment-remind and 1 payment-final delayed publish, got remind=%d final=%d", remindCount, finalCount)
	}

	var linkStatus string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.payment_links WHERE invoice_id = $1`, invID).Scan(&linkStatus)
	if linkStatus != "failed" {
		t.Errorf("payment_link: want status=failed, got %q", linkStatus)
	}
}

// TestIntegration_StripeWebhook_PaymentIntentFailed_ResolvesByInvoiceMetadata
// guards against a regression where payment_intent.payment_failed events
// were routed through the same external_id lookup as checkout.session.*
// events. The data object for that event type is a PaymentIntent (pi_...),
// a different ID namespace than the Checkout Session ID (cs_...) stored in
// payment_links.external_id — so external_id could never match, and the
// failure was silently ignored (subscription stayed active, no past_due,
// no dunning). The payload's data.object.id here is deliberately NOT the
// seeded external_id, proving resolution goes through metadata.invoice_id.
func TestIntegration_StripeWebhook_PaymentIntentFailed_ResolvesByInvoiceMetadata(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user      = "integ_billing_wh_pi_failed_user"
		orgID     = "00000000-0000-0000-0000-000000000f09"
		sessionID = "cs_test_f09" // the Checkout Session ID, stored as external_id
		piID      = "pi_test_f09" // the PaymentIntent ID Stripe's event actually carries
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	pool.Exec(t.Context(), `UPDATE billing.subscriptions SET status = 'active' WHERE subject_type = 'organization' AND subject_id = $1`, orgID)

	subID := getSubscriptionID(pool, orgID)
	invID := seedInvoice(pool, subID, "USD", 900)
	seedPaymentLink(pool, invID, sessionID, "stripe", "USD", 900)

	payload := `{"type":"payment_intent.payment_failed","data":{"object":{"id":"` + piID + `","metadata":{"invoice_id":"` + invID + `"}}}}`
	e := billing.NewWebhookModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if w.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", w.Code, w.Body)
	}

	var linkStatus string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.payment_links WHERE invoice_id = $1`, invID).Scan(&linkStatus)
	if linkStatus != "failed" {
		t.Errorf("payment_link: want status=failed, got %q", linkStatus)
	}

	var subStatus string
	pool.QueryRow(t.Context(), `SELECT status FROM billing.subscriptions WHERE id = $1`, subID).Scan(&subStatus)
	if subStatus != "past_due" {
		t.Errorf("subscription: want status=past_due after payment_intent.payment_failed, got %q", subStatus)
	}
}

// --- checkUsageLimit exceeded ---

// TestIntegration_ProvisionSubscription_SeedsMemberUsage regression-tests
// that provisioning must seed billing.usage with the owner's own seat
// (members=1) synchronously, in the same transaction as the subscription
// insert — not leave it to the async syncMemberUsage sync that only runs
// after a *subsequent* member is added. Otherwise, checkUsageLimit
// would hit its ErrNoRows fail-open path and report current=0 for a brand-new
// organization, letting the first invite/add on a 1-seat plan through
// despite the owner already occupying that one seat.
func TestIntegration_ProvisionSubscription_SeedsMemberUsage(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_seed_usage_user"
		orgID = "00000000-0000-0000-0000-000000000f39"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	// No manual usage seeding here — this is the first org for this user
	// (trialing, per provisionSubscription's count==0 branch), the exact
	// shape of the originally-reported bug. The seed must land inside
	// provisionSubscription itself.
	current, limit, err := mod.CheckUsageLimit(t.Context(), orgID, "members")
	if err != nil {
		t.Fatalf("CheckUsageLimit: %v", err)
	}
	if current != 1 {
		t.Errorf("want current=1 (the owner's own seat) immediately after provisioning, got %d", current)
	}
	// solo plan's members limit is 1 (seed data) — current >= limit means
	// organization.addMember/createInvitation's existing enforcement check
	// now correctly rejects a first add/invite instead of letting it
	// through against a stale current=0.
	if limit >= 0 && current < int64(limit) {
		t.Errorf("want current (%d) >= limit (%d) — a first add should now be correctly blocked", current, limit)
	}
}

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

	// cancelOnDeletion is a distinct method from the owner-facing
	// cancelSubscription and must stay reason-less — there's no owner
	// interaction to capture one from. Confirms it's untouched by the new
	// reason/details plumbing, not just assumed unaffected.
	var metadataRaw []byte
	if err := pool.QueryRow(t.Context(),
		`SELECT metadata FROM billing.subscription_history WHERE subscription_id=(SELECT id FROM billing.subscriptions WHERE subject_id=$1) AND action='cancel' ORDER BY changed_at DESC LIMIT 1`,
		orgID).Scan(&metadataRaw); err != nil {
		t.Fatalf("query subscription_history: %v", err)
	}
	if string(metadataRaw) != "{}" {
		t.Errorf("cancelOnDeletion history metadata: want empty {}, got %s", metadataRaw)
	}
}

// TestIntegration_CancelOnDeletion_HistoryFailure_RollsBackStatus proves
// cancelOnDeletion's status update and history insert are atomic: if the
// history insert fails, the status change must not have committed either.
func TestIntegration_CancelOnDeletion_HistoryFailure_RollsBackStatus(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_ws_deleted_txfail_user"
		orgID = "00000000-0000-0000-0000-000000000f1a"
	)
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	subID := getSubscriptionID(pool, orgID)
	forceHistoryInsertFailure(t, pool, subID)

	deletedEvt := events.Envelope{
		ID: "del-txfail-" + orgID, Type: events.RoutingKeyOrganizationDeleted,
		Source: "organization", Time: time.Now(), OrgID: orgID,
		Data: events.OrganizationDeleted{OrganizationID: orgID, DeletedAt: time.Now()},
	}
	body, _ := json.Marshal(deletedEvt)

	if err := mod.Worker.HandleOrganizationDeleted(t.Context(), body); err == nil {
		t.Fatal("HandleOrganizationDeleted: want error from failed history insert, got nil")
	}

	var status string
	pool.QueryRow(t.Context(),
		`SELECT status FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1`, orgID,
	).Scan(&status)
	if status == "cancelled" {
		t.Errorf("after failed history insert: want status not cancelled, got %q", status)
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
	expectedEnd, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, expectedEnd, isTrial)
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

// TestIntegration_RedeemCoupon_ConcurrentRedemptions_OnlyOneWins above races
// two *different* coupon codes against one subscription, exercising
// lockSubscriptionForUpdate's "one active coupon per subscription" guard.
// max_redemptions is a *global* counter on the coupon row, shared across
// every subscription that redeems it — a subscription-scoped lock can't
// serialize that, so this races the *same* coupon code, with
// max_redemptions=1, across two different organizations' subscriptions to
// exercise tryIncrementCouponRedeemedCount's atomic UPDATE ... WHERE
// redeemed_count < max_redemptions directly.
func TestIntegration_RedeemCoupon_ConcurrentRedemptions_SameCode_GlobalMaxRedemptionsEnforced(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		userA = "integ_billing_coupon_global_race_user_a"
		userB = "integ_billing_coupon_global_race_user_b"
		orgA  = "00000000-0000-0000-0000-000000000f32"
		orgB  = "00000000-0000-0000-0000-000000000f33"
		code  = "GLOBALMAX_F32"
	)
	setupBillingTest(t, pool, orgA)
	setupBillingTest(t, pool, orgB)
	one := 1
	seedCoupon(t, pool, code, couponSeedOpts{DiscountType: "fixed", AmountCents: couponAmount(100), Cadence: "forever", MaxRedemptions: &one})

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgA, userA)); err != nil {
		t.Fatalf("provision org A: %v", err)
	}
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgB, userB)); err != nil {
		t.Fatalf("provision org B: %v", err)
	}

	engines := []*gin.Engine{
		billing.NewModuleEngineWithRealRLS(pool, userA, orgA, stubRefReader{}),
		billing.NewModuleEngineWithRealRLS(pool, userB, orgB, stubRefReader{}),
	}
	orgIDs := []string{orgA, orgB}
	results := make([]int, len(engines))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range engines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			w := httptest.NewRecorder()
			engines[i].ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgIDs[i])+"/coupons/redeem", `{"code":"`+code+`"}`))
			results[i] = w.Code
		}(i)
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
		t.Errorf("want exactly 1 of 2 concurrent redemptions of a max_redemptions=1 coupon to succeed, got %d (statuses: %v)", successCount, results)
	}

	var redeemedCount int
	pool.QueryRow(t.Context(), `SELECT redeemed_count FROM billing.coupons WHERE code = $1`, code).Scan(&redeemedCount)
	if redeemedCount != 1 {
		t.Errorf("want redeemed_count=1 after the race (not over-redeemed), got %d", redeemedCount)
	}

	var totalRedemptions int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM billing.coupon_redemptions WHERE coupon_id = $1`, code).Scan(&totalRedemptions)
	if totalRedemptions != 1 {
		t.Errorf("want exactly 1 redemption row across both organizations, got %d", totalRedemptions)
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
	if w.Code != http.StatusOK {
		t.Fatalf("attach: want 200, got %d: %s", w.Code, w.Body)
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
	expectedEnd, isTrial := getSubscriptionExpectedEnd(pool, subID)
	body := encodeSubscriptionCheckEvent(subID, orgID, expectedEnd, isTrial)
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

// TestIntegration_HandleOrganizationCreated_InvoiceNumberDoesNotCollideAcrossOrgs
// regression-tests that invoice_number is generated from a per-organization
// counter (billing.invoice_sequences), so two *different* organizations each
// provisioning straight to active (no trial — see the wsID1/wsID2 pattern used
// elsewhere in this file) independently compute seq=1 for their own first
// invoice. The constraint is scoped to (subscription_id, invoice_number),
// so both must succeed with the identical formatted number.
func TestIntegration_HandleOrganizationCreated_InvoiceNumberDoesNotCollideAcrossOrgs(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		userA = "integ_billing_invnum_a"
		wsA1  = "00000000-0000-0000-0000-000000000f35"
		wsA2  = "00000000-0000-0000-0000-000000000f36"
		userB = "integ_billing_invnum_b"
		wsB1  = "00000000-0000-0000-0000-000000000f37"
		wsB2  = "00000000-0000-0000-0000-000000000f38"
	)
	for _, id := range []string{wsA1, wsA2, wsB1, wsB2} {
		cleanupBillingByOrganization(pool, id)
	}
	t.Cleanup(func() {
		for _, id := range []string{wsA1, wsA2, wsB1, wsB2} {
			cleanupBillingByOrganization(pool, id)
		}
	})
	for _, id := range []string{wsA1, wsA2} {
		seedBillingOrganization(pool, id, userA)
	}
	for _, id := range []string{wsB1, wsB2} {
		seedBillingOrganization(pool, id, userB)
	}

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(organization.New(pool, messaging.NoopPublisher{}))
	// wsA1/wsB1 each absorb their own user's one-per-user trial; wsA2/wsB2
	// each provision straight to active — both computing seq=1 independently.
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsA1, userA)); err != nil {
		t.Fatalf("provision wsA1: %v", err)
	}
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsA2, userA)); err != nil {
		t.Fatalf("provision wsA2 (org A's active org — the one that generates seq=1): %v", err)
	}
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsB1, userB)); err != nil {
		t.Fatalf("provision wsB1: %v", err)
	}
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsB2, userB)); err != nil {
		t.Fatalf("provision wsB2 (org B's active org — must NOT collide with org A's seq=1): %v", err)
	}

	var numA, numB string
	pool.QueryRow(t.Context(),
		`SELECT invoice_number FROM billing.invoices WHERE subscription_id = (SELECT id FROM billing.subscriptions WHERE subject_id = $1)`, wsA2,
	).Scan(&numA)
	pool.QueryRow(t.Context(),
		`SELECT invoice_number FROM billing.invoices WHERE subscription_id = (SELECT id FROM billing.subscriptions WHERE subject_id = $1)`, wsB2,
	).Scan(&numB)
	if numA == "" || numB == "" {
		t.Fatalf("want both orgs to have an invoice_number, got A=%q B=%q", numA, numB)
	}
	if numA != numB {
		t.Errorf("want both orgs' first invoice to independently compute the same formatted number (proving the scope is per-subscription, not incidentally different) — got A=%q B=%q", numA, numB)
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
	payProvisioningInvoice(pool, wsID2)

	// NewWebhookModuleEngine (not NewModuleEngine) — this test needs the
	// public /webhooks/stripe route to simulate the paid callback below.
	e := billing.NewWebhookModuleEngine(pool, user, wsID2)

	// Captured through the same JSON API path used for the post-extend
	// comparisons below (not a raw SQL scan) so both sides are the same
	// serialized format — timestamptz text and the API's JSON time encoding
	// don't match byte-for-byte even for the same instant.
	beforePeriodEnd := getSubscriptionData(t, e, wsID2)["period_end"]

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"months":1}`))
	if w.Code != http.StatusOK {
		t.Fatalf("extend: want 200, got %d: %s", w.Code, w.Body)
	}

	var resp struct {
		Data struct {
			ID          string `json:"id"`
			Kind        string `json:"kind"`
			AmountCents int64  `json:"amount_cents"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Data.AmountCents != 900 { // solo plan: $9/mo monthly price (seed data)
		t.Errorf("extend invoice: want amount_cents=900 (1 month at solo's $9/mo), got %d", resp.Data.AmountCents)
	}
	if resp.Data.Kind != "extension" {
		t.Errorf("extend invoice: want kind='extension', got %q", resp.Data.Kind)
	}

	// period_end must stay put until the extension invoice is actually paid.
	data := getSubscriptionData(t, e, wsID2)
	if data["period_end"] != beforePeriodEnd {
		t.Error("want period_end unchanged before payment")
	}

	// A second extend while the first invoice is still pending must be rejected.
	w2 := httptest.NewRecorder()
	e.ServeHTTP(w2, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"months":1}`))
	if w2.Code != http.StatusUnprocessableEntity {
		t.Fatalf("extend second call: want 422, got %d: %s", w2.Code, w2.Body)
	}

	// extendSubscription's own payment-link creation hits the real Stripe API
	// and fails in this environment (no credentials configured) — seed one
	// directly instead, same as every other webhook-path test in this file
	// (e.g. TestIntegration_StripeWebhook_MarksInvoicePaid). No event "id" in
	// the payload — same as every other webhook test here — since a fixed
	// event ID would collide with billing.webhook_events across repeated test
	// runs (cleanupBillingByOrganization only prunes rows whose event_id is
	// prefixed by this org's own payment-link external_id).
	const extID = "stripe_sess_extend_happy"
	seedPaymentLink(pool, resp.Data.ID, extID, "stripe", "USD", resp.Data.AmountCents)
	payload := fmt.Sprintf(`{"type":"checkout.session.completed","data":{"object":{"id":%q,"payment_status":"paid"}}}`, extID)

	wWeb := httptest.NewRecorder()
	e.ServeHTTP(wWeb, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if wWeb.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", wWeb.Code, wWeb.Body)
	}

	dataAfter := getSubscriptionData(t, e, wsID2)
	if dataAfter["period_end"] == beforePeriodEnd {
		t.Error("want period_end to move forward after paid webhook")
	}

	var historyCount int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.subscription_history WHERE subscription_id = (SELECT id FROM billing.subscriptions WHERE subject_id = $1) AND action = 'extend'`, wsID2,
	).Scan(&historyCount)
	if historyCount != 1 {
		t.Errorf("want 1 'extend' history row, got %d", historyCount)
	}
}

func TestIntegration_ExtendSubscription_PaidWebhookWhenExpired(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_extend_expired"
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
	_ = mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user))
	_ = mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID2, user))
	payProvisioningInvoice(pool, wsID2)

	e := billing.NewWebhookModuleEngine(pool, user, wsID2)

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"months":1}`))
	if w.Code != http.StatusOK {
		t.Fatalf("extend: want 200, got %d", w.Code)
	}

	var resp struct {
		Data struct {
			ID          string `json:"id"`
			AmountCents int64  `json:"amount_cents"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)

	pool.Exec(t.Context(), `UPDATE billing.subscriptions SET status = 'expired' WHERE subject_id = $1`, wsID2)

	const extID = "stripe_sess_extend_expired"
	seedPaymentLink(pool, resp.Data.ID, extID, "stripe", "USD", resp.Data.AmountCents)
	// No event "id" — see the matching comment in TestIntegration_ExtendSubscription_HappyPath.
	payload := fmt.Sprintf(`{"type":"checkout.session.completed","data":{"object":{"id":%q,"payment_status":"paid"}}}`, extID)

	wWeb := httptest.NewRecorder()
	e.ServeHTTP(wWeb, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if wWeb.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d", wWeb.Code)
	}

	dataAfter := getSubscriptionData(t, e, wsID2)
	if dataAfter["status"] != "active" {
		t.Errorf("want status=active after paid webhook on expired sub, got %v", dataAfter["status"])
	}

	var historyCount int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.subscription_history WHERE subscription_id = (SELECT id FROM billing.subscriptions WHERE subject_id = $1) AND action = 'resume'`, wsID2,
	).Scan(&historyCount)
	if historyCount != 1 {
		t.Errorf("want 1 'resume' history row, got %d", historyCount)
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
	// (insertInvoiceLineItems) to prove the earlier writes (invoice)
	// in the same transaction roll back with it.
	if _, err := pool.Exec(t.Context(), `
		CREATE OR REPLACE FUNCTION billing.__test_reject_sentinel_line_items() RETURNS trigger AS $$
		BEGIN
			IF NEW.invoice_id IN (SELECT id FROM billing.invoices WHERE status = 'pending') THEN
				RAISE EXCEPTION 'simulated mid-sequence failure';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create trigger function: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		CREATE TRIGGER __test_reject_sentinel_line_items BEFORE INSERT ON billing.invoice_line_items
		FOR EACH ROW EXECUTE FUNCTION billing.__test_reject_sentinel_line_items()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS __test_reject_sentinel_line_items ON billing.invoice_line_items`)
		pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS billing.__test_reject_sentinel_line_items()`)
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

	// Runway-cap model: period_end may never sit more than 24 calendar
	// months ahead of now, regardless of created_at. Push period_end out to
	// exactly that horizon so any further extension is rejected outright.
	pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET period_end = now() + interval '24 months' WHERE subject_type = 'organization' AND subject_id = $1`,
		wsID2)

	e := billing.NewModuleEngine(pool, user, wsID2, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"months":1}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("extend past 24-month runway cap: want 422, got %d: %s", w.Code, w.Body)
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

// --- switch to annual + tiered pricing ---

// TestIntegration_ExtendSubscription_SwitchToAnnual_HappyPath verifies the
// full async round trip for the switch-to-annual flow: switch_to_annual is
// persisted on the invoice at request time, has no effect until the invoice
// is actually paid, and only the webhook's payment-confirmation path (not
// extendSubscription itself) ever touches cycle.
func TestIntegration_ExtendSubscription_SwitchToAnnual_HappyPath(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_switchannual_user"
		wsID1 = "00000000-0000-0000-0000-000000000f70"
		wsID2 = "00000000-0000-0000-0000-000000000f71"
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
	payProvisioningInvoice(pool, wsID2)

	e := billing.NewWebhookModuleEngine(pool, user, wsID2)

	before := getSubscriptionData(t, e, wsID2)
	if before["cycle"] != "monthly" {
		t.Fatalf("precondition: want cycle=monthly, got %v", before["cycle"])
	}
	beforePeriodEnd, err := time.Parse(time.RFC3339, before["period_end"].(string))
	if err != nil {
		t.Fatalf("parse before period_end: %v", err)
	}

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"switch_to_annual":true}`))
	if w.Code != http.StatusOK {
		t.Fatalf("extend switch_to_annual: want 200, got %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Data struct {
			ID          string `json:"id"`
			Kind        string `json:"kind"`
			AmountCents int64  `json:"amount_cents"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Data.AmountCents != 9000 { // solo plan: $90/yr (seed data) — one yearly block, not 12x monthly
		t.Errorf("switch_to_annual invoice: want amount_cents=9000, got %d", resp.Data.AmountCents)
	}

	// cycle (and period) must not move until the invoice is actually paid —
	// the whole point of moving the branch out of extendSubscription.
	mid := getSubscriptionData(t, e, wsID2)
	if mid["cycle"] != "monthly" {
		t.Errorf("want cycle unchanged before payment, got %v", mid["cycle"])
	}
	if mid["period_end"] != before["period_end"] {
		t.Error("want period_end unchanged before payment")
	}

	const extID = "stripe_sess_switch_annual_happy"
	seedPaymentLink(pool, resp.Data.ID, extID, "stripe", "USD", resp.Data.AmountCents)
	payload := fmt.Sprintf(`{"type":"checkout.session.completed","data":{"object":{"id":%q,"payment_status":"paid"}}}`, extID)
	wWeb := httptest.NewRecorder()
	e.ServeHTTP(wWeb, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if wWeb.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", wWeb.Code, wWeb.Body)
	}

	after := getSubscriptionData(t, e, wsID2)
	if after["cycle"] != "yearly" {
		t.Errorf("want cycle=yearly after paid webhook, got %v", after["cycle"])
	}
	afterPeriodEnd, err := time.Parse(time.RFC3339, after["period_end"].(string))
	if err != nil {
		t.Fatalf("parse after period_end: %v", err)
	}
	if want := beforePeriodEnd.AddDate(0, 12, 0); !afterPeriodEnd.Equal(want) {
		t.Errorf("want period_end advanced by exactly 12 months to %v, got %v", want, afterPeriodEnd)
	}
}

// TestIntegration_ExtendSubscription_SwitchToAnnual_RejectedWhenAlreadyYearly
// confirms the switch-to-annual rejection is enforced server-side —
// rejected regardless of what the UI would have shown, not just hidden
// client-side.
func TestIntegration_ExtendSubscription_SwitchToAnnual_RejectedWhenAlreadyYearly(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_switchannual_yearly_user"
		wsID1 = "00000000-0000-0000-0000-000000000f72"
		wsID2 = "00000000-0000-0000-0000-000000000f73"
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
	if err := mod.Worker.HandleOrganizationCreated(t.Context(),
		encodeOrganizationCreatedEventWithPlan(wsID2, user, "solo", "yearly")); err != nil {
		t.Fatalf("second provision (yearly): %v", err)
	}

	e := billing.NewModuleEngine(pool, user, wsID2, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"switch_to_annual":true}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("switch_to_annual on already-yearly sub: want 422, got %d: %s", w.Code, w.Body)
	}
}

// TestIntegration_ExtendSubscription_UnpaidSwitchToAnnual_LeavesCycleUntouched
// confirms an abandoned switch-to-annual invoice (created, never paid) has
// no effect at all — the cycle write only happens in handleWebhook.
func TestIntegration_ExtendSubscription_UnpaidSwitchToAnnual_LeavesCycleUntouched(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_switchannual_unpaid_user"
		wsID1 = "00000000-0000-0000-0000-000000000f82"
		wsID2 = "00000000-0000-0000-0000-000000000f83"
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
	payProvisioningInvoice(pool, wsID2)

	e := billing.NewModuleEngine(pool, user, wsID2, stubRefReader{})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"switch_to_annual":true}`))
	if w.Code != http.StatusOK {
		t.Fatalf("extend switch_to_annual: want 200, got %d: %s", w.Code, w.Body)
	}

	data := getSubscriptionData(t, e, wsID2)
	if data["cycle"] != "monthly" {
		t.Errorf("want cycle unchanged for an unpaid switch_to_annual invoice, got %v", data["cycle"])
	}

	var historyCount int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.subscription_history WHERE subscription_id = (SELECT id FROM billing.subscriptions WHERE subject_id = $1) AND action = 'extend'`, wsID2,
	).Scan(&historyCount)
	if historyCount != 0 {
		t.Errorf("want no 'extend' history row before payment, got %d", historyCount)
	}
}

// TestIntegration_ExtendSubscription_TieredPricing_13Months guards against
// handleWebhook reading only lineItems[0].Quantity (which would apply 12
// months, the block line item's quantity, not the correct 13) instead of
// summing every line item's quantity.
func TestIntegration_ExtendSubscription_TieredPricing_13Months(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_tiered13_user"
		wsID1 = "00000000-0000-0000-0000-000000000f74"
		wsID2 = "00000000-0000-0000-0000-000000000f75"
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
	payProvisioningInvoice(pool, wsID2)

	e := billing.NewWebhookModuleEngine(pool, user, wsID2)
	before := getSubscriptionData(t, e, wsID2)
	beforePeriodEnd, err := time.Parse(time.RFC3339, before["period_end"].(string))
	if err != nil {
		t.Fatalf("parse before period_end: %v", err)
	}

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"months":13}`))
	if w.Code != http.StatusOK {
		t.Fatalf("extend 13 months: want 200, got %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Data struct {
			ID          string `json:"id"`
			AmountCents int64  `json:"amount_cents"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Data.AmountCents != 9900 { // 1 yearly block ($90) + 1 month ($9) = $99
		t.Errorf("13-month extend: want amount_cents=9900 (1 yearly block + 1 month), got %d", resp.Data.AmountCents)
	}

	// Two line items, each internally consistent (unit_price * quantity ==
	// total_cents) — the numbers an invoice PDF renders must add up.
	rows, err := pool.Query(t.Context(),
		`SELECT quantity, unit_price_cents, total_cents FROM billing.invoice_line_items WHERE invoice_id = $1 ORDER BY sort_order`, resp.Data.ID)
	if err != nil {
		t.Fatalf("query line items: %v", err)
	}
	defer rows.Close()
	type li struct{ qty, unit, total int64 }
	var items []li
	for rows.Next() {
		var it li
		if err := rows.Scan(&it.qty, &it.unit, &it.total); err != nil {
			t.Fatalf("scan line item: %v", err)
		}
		items = append(items, it)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 line items for a 13-month tiered extension, got %d: %+v", len(items), items)
	}
	if items[0].qty != 12 || items[0].total != 9000 || items[0].unit*items[0].qty != items[0].total {
		t.Errorf("block line item: want qty=12 total=9000 unit*qty==total, got %+v", items[0])
	}
	if items[1].qty != 1 || items[1].unit != 900 || items[1].total != 900 {
		t.Errorf("remainder line item: want qty=1 unit=900 total=900, got %+v", items[1])
	}
	if items[0].qty+items[1].qty != 13 {
		t.Errorf("want line item quantities to sum to 13 months, got %d", items[0].qty+items[1].qty)
	}

	const extID = "stripe_sess_tiered13"
	seedPaymentLink(pool, resp.Data.ID, extID, "stripe", "USD", resp.Data.AmountCents)
	payload := fmt.Sprintf(`{"type":"checkout.session.completed","data":{"object":{"id":%q,"payment_status":"paid"}}}`, extID)
	wWeb := httptest.NewRecorder()
	e.ServeHTTP(wWeb, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if wWeb.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", wWeb.Code, wWeb.Body)
	}

	after := getSubscriptionData(t, e, wsID2)
	afterPeriodEnd, err := time.Parse(time.RFC3339, after["period_end"].(string))
	if err != nil {
		t.Fatalf("parse after period_end: %v", err)
	}
	if want := beforePeriodEnd.AddDate(0, 13, 0); !afterPeriodEnd.Equal(want) {
		t.Errorf("want period_end advanced by exactly 13 months (summed across both line items) to %v, got %v — a regression to lineItems[0].Quantity alone would advance by only 12", want, afterPeriodEnd)
	}
	if after["cycle"] != "monthly" {
		t.Errorf("a plain (non switch_to_annual) tiered extend must not change cycle, got %v", after["cycle"])
	}
}

// TestIntegration_ExtendSubscription_BillsAttachedAddon guards the actual
// money leak this was written for: before this fix, an extension invoice
// priced the plan only, and since addons ride the subscription's period_end
// with no expiry of their own, the regular renewal (the only thing that
// would otherwise bill them) got rescheduled past the bought window —
// letting an attached addon run free for the length of the extension. It
// also regression-tests the extension_months fix: naively summing every
// line item's quantity (the old months-recovery method) would double-count
// once addon lines (also quantified in months) share the invoice with the
// plan's own lines.
func TestIntegration_ExtendSubscription_BillsAttachedAddon(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_extend_addon_user"
		wsID1 = "00000000-0000-0000-0000-000000000f76"
		wsID2 = "00000000-0000-0000-0000-000000000f77"
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
	payProvisioningInvoice(pool, wsID2)

	subID := getSubscriptionID(pool, wsID2)
	// extra-seat: USD monthly=100, yearly=1000 (db/migrations/000004_billing.up.sql
	// seed data) — attached directly at quantity 2, bypassing the
	// pay-to-attach flow, since only the live quantity at extend time matters
	// here, not how it got there.
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO billing.subscription_addons (subscription_id, addon_id, quantity) VALUES ($1, 'extra-seat', 2)`,
		subID); err != nil {
		t.Fatalf("seed attached addon: %v", err)
	}

	e := billing.NewWebhookModuleEngine(pool, user, wsID2)
	before := getSubscriptionData(t, e, wsID2)
	beforePeriodEnd, err := time.Parse(time.RFC3339, before["period_end"].(string))
	if err != nil {
		t.Fatalf("parse before period_end: %v", err)
	}

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"months":13}`))
	if w.Code != http.StatusOK {
		t.Fatalf("extend 13 months: want 200, got %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Data struct {
			ID          string `json:"id"`
			AmountCents int64  `json:"amount_cents"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	// Plan: 1 yearly block ($90) + 1 month ($9) = $99 (same as the
	// no-addon 13-month case). Addon: (1 yearly block $10 + 1 month $1) x
	// quantity 2 = $22. Total = $99 + $22 = $121 — the addon leak this test
	// exists to close would leave amount_cents at 9900, plan only.
	const want = 9900 + 2200
	if resp.Data.AmountCents != want {
		t.Errorf("13-month extend with attached addon: want amount_cents=%d (plan + addon), got %d", want, resp.Data.AmountCents)
	}

	rows, err := pool.Query(t.Context(),
		`SELECT quantity, unit_price_cents, total_cents FROM billing.invoice_line_items WHERE invoice_id = $1 ORDER BY sort_order`, resp.Data.ID)
	if err != nil {
		t.Fatalf("query line items: %v", err)
	}
	defer rows.Close()
	type li struct{ qty, unit, total int64 }
	var items []li
	for rows.Next() {
		var it li
		if err := rows.Scan(&it.qty, &it.unit, &it.total); err != nil {
			t.Fatalf("scan line item: %v", err)
		}
		items = append(items, it)
	}
	if len(items) != 4 {
		t.Fatalf("want 4 line items (plan block, plan remainder, addon block, addon remainder), got %d: %+v", len(items), items)
	}
	// unit_price_cents is a monthly-equivalent display rate (total/qty,
	// floor-rounded) — total_cents is the actual charge and isn't
	// guaranteed to equal unit*qty when the division isn't exact (same
	// accepted rounding as the plan's own block line, see
	// insertExtensionLineItems), so only qty/total are asserted here.
	if items[2].qty != 12 || items[2].total != 2000 {
		t.Errorf("addon block line item: want qty=12 total=2000, got %+v", items[2])
	}
	if items[3].qty != 1 || items[3].unit != 200 || items[3].total != 200 {
		t.Errorf("addon remainder line item: want qty=1 unit=200 total=200, got %+v", items[3])
	}

	var extensionMonths *int
	if err := pool.QueryRow(t.Context(),
		`SELECT extension_months FROM billing.invoices WHERE id = $1`, resp.Data.ID,
	).Scan(&extensionMonths); err != nil {
		t.Fatalf("query extension_months: %v", err)
	}
	if extensionMonths == nil || *extensionMonths != 13 {
		t.Errorf("want invoices.extension_months=13 (not derived from summing all 4 line items' quantities, which would be 26), got %v", extensionMonths)
	}

	const extID = "stripe_sess_extend_addon"
	seedPaymentLink(pool, resp.Data.ID, extID, "stripe", "USD", resp.Data.AmountCents)
	payload := fmt.Sprintf(`{"type":"checkout.session.completed","data":{"object":{"id":%q,"payment_status":"paid"}}}`, extID)
	wWeb := httptest.NewRecorder()
	e.ServeHTTP(wWeb, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if wWeb.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", wWeb.Code, wWeb.Body)
	}

	after := getSubscriptionData(t, e, wsID2)
	afterPeriodEnd, err := time.Parse(time.RFC3339, after["period_end"].(string))
	if err != nil {
		t.Fatalf("parse after period_end: %v", err)
	}
	// The regression this guards against: summing all 4 line items'
	// quantities (12+1+12+1=26) instead of reading extension_months back
	// would double the period advance to 26 months instead of 13.
	if want := beforePeriodEnd.AddDate(0, 13, 0); !afterPeriodEnd.Equal(want) {
		t.Errorf("want period_end advanced by exactly 13 months to %v, got %v — a regression to summing every line item's quantity would advance by 26", want, afterPeriodEnd)
	}
}

// TestIntegration_ExtendSubscription_PlainTwelveMonths_DoesNotChangeCycle
// confirms a plain 12-month extend, which prices identically to
// switch-to-annual (one yearly block), must never touch cycle — only the
// switch_to_annual request path does.
func TestIntegration_ExtendSubscription_PlainTwelveMonths_DoesNotChangeCycle(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_plain12_user"
		wsID1 = "00000000-0000-0000-0000-000000000f76"
		wsID2 = "00000000-0000-0000-0000-000000000f77"
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
	payProvisioningInvoice(pool, wsID2)

	e := billing.NewWebhookModuleEngine(pool, user, wsID2)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"months":12}`))
	if w.Code != http.StatusOK {
		t.Fatalf("extend 12 months: want 200, got %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Data struct {
			ID          string `json:"id"`
			AmountCents int64  `json:"amount_cents"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Data.AmountCents != 9000 {
		t.Errorf("plain 12-month extend: want amount_cents=9000 (same price as switch_to_annual), got %d", resp.Data.AmountCents)
	}

	const extID = "stripe_sess_plain12"
	seedPaymentLink(pool, resp.Data.ID, extID, "stripe", "USD", resp.Data.AmountCents)
	payload := fmt.Sprintf(`{"type":"checkout.session.completed","data":{"object":{"id":%q,"payment_status":"paid"}}}`, extID)
	wWeb := httptest.NewRecorder()
	e.ServeHTTP(wWeb, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
	if wWeb.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", wWeb.Code, wWeb.Body)
	}

	after := getSubscriptionData(t, e, wsID2)
	if after["cycle"] != "monthly" {
		t.Errorf("plain 12-month extend must not change cycle even though it prices like a yearly block, got %v", after["cycle"])
	}
}

// TestIntegration_ExtendSubscription_PendingGuard_BlocksBothVariantsEitherDirection
// confirms the pending-invoice guard needs no new logic to cover both
// invoice variants, because both keep kind="extension" — a pending plain
// extend blocks a switch-to-annual attempt and vice versa.
func TestIntegration_ExtendSubscription_PendingGuard_BlocksBothVariantsEitherDirection(t *testing.T) {
	pool := testPoolBilling(t)

	t.Run("pending plain extend blocks a switch_to_annual attempt", func(t *testing.T) {
		const (
			user  = "integ_billing_pendingguard_a_user"
			wsID1 = "00000000-0000-0000-0000-000000000f78"
			wsID2 = "00000000-0000-0000-0000-000000000f79"
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
		payProvisioningInvoice(pool, wsID2)

		e := billing.NewModuleEngine(pool, user, wsID2, stubRefReader{})
		w1 := httptest.NewRecorder()
		e.ServeHTTP(w1, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"months":1}`))
		if w1.Code != http.StatusOK {
			t.Fatalf("first (plain) extend: want 200, got %d: %s", w1.Code, w1.Body)
		}

		w2 := httptest.NewRecorder()
		e.ServeHTTP(w2, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"switch_to_annual":true}`))
		if w2.Code != http.StatusUnprocessableEntity {
			t.Errorf("switch_to_annual while a plain extend is pending: want 422, got %d: %s", w2.Code, w2.Body)
		}
	})

	t.Run("pending switch_to_annual blocks a plain extend attempt", func(t *testing.T) {
		const (
			user  = "integ_billing_pendingguard_b_user"
			wsID1 = "00000000-0000-0000-0000-000000000f80"
			wsID2 = "00000000-0000-0000-0000-000000000f81"
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
		payProvisioningInvoice(pool, wsID2)

		e := billing.NewModuleEngine(pool, user, wsID2, stubRefReader{})
		w1 := httptest.NewRecorder()
		e.ServeHTTP(w1, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"switch_to_annual":true}`))
		if w1.Code != http.StatusOK {
			t.Fatalf("first (switch_to_annual) extend: want 200, got %d: %s", w1.Code, w1.Body)
		}

		w2 := httptest.NewRecorder()
		e.ServeHTTP(w2, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID2)+"/extend", `{"months":1}`))
		if w2.Code != http.StatusUnprocessableEntity {
			t.Errorf("plain extend while switch_to_annual is pending: want 422, got %d: %s", w2.Code, w2.Body)
		}
	})
}

// TestIntegration_GetSubscription_MaxExtendableMonths verifies that the
// field the frontend gates the whole Extend UI on actually appears,
// correctly, in a real response.
func TestIntegration_GetSubscription_MaxExtendableMonths(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_maxextend_user"
		wsID1 = "00000000-0000-0000-0000-000000000f84"
		wsID2 = "00000000-0000-0000-0000-000000000f85"
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

	data := getSubscriptionData(t, billing.NewModuleEngine(pool, user, wsID2, stubRefReader{}), wsID2)
	got, ok := data["max_extendable_months"].(float64) // JSON numbers decode as float64 into map[string]any
	if !ok {
		t.Fatalf("want max_extendable_months present as a number, got %#v", data["max_extendable_months"])
	}
	// Not asserting the exact value: unlike TestMaxExtendableMonths (which
	// uses fixed dates to prove the arithmetic exactly), this runs against
	// the real current time, so the precise number legitimately varies by a
	// day depending on where "now" falls relative to period_end's
	// time-of-day. This test only needs to confirm the field is wired end
	// to end and in a sane range for a subscription that was just created
	// (monthly cycle, period_end ~ now+1mo, so ~23 of the 24-month runway
	// cap remains).
	if got < 20 || got > 24 {
		t.Errorf("a brand new active subscription: want max_extendable_months in [20,24], got %v", got)
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
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel", `{"reason":"too_expensive"}`))

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

// TestIntegration_PreviewInvoice_SameCycleSwitch_NewPeriodEndUnchanged
// mirrors TestIntegration_ChangePlan_SameCycleSwitch_LeavesPeriodEndUnchanged
// on the read-only preview path: the preview must never promise a prorated
// period_end that the real changePlanWithMetadata call would no longer
// produce for a same-plan cycle-only switch.
func TestIntegration_PreviewInvoice_SameCycleSwitch_NewPeriodEndUnchanged(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_preview_cycleswitch_user"
		orgID = "00000000-0000-0000-0000-000000000f32"
	)
	setupBillingTest(t, pool, orgID)
	subID := seedActiveOrgNoSchedule(t, pool, orgID, user)
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET plan = 'growth', cycle = 'monthly', period_end = now() + interval '700 days' WHERE id = $1`,
		subID); err != nil {
		t.Fatalf("seed banked-runway subscription: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID, stubRefReader{})
	before := getSubscriptionData(t, e, orgID)
	beforePeriodEnd, err := time.Parse(time.RFC3339, before["period_end"].(string))
	if err != nil {
		t.Fatalf("parse before period_end: %v", err)
	}

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(orgID)+"/preview?plan=growth&cycle=yearly", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("preview: want 200, got %d: %s", w.Code, w.Body)
	}

	var resp struct {
		Data struct {
			NewPeriodEnd *string `json:"new_period_end"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Data.NewPeriodEnd == nil {
		t.Fatal("preview same-plan cycle switch: want new_period_end set (to the unchanged date), got nil")
	}
	previewedPeriodEnd, err := time.Parse(time.RFC3339, *resp.Data.NewPeriodEnd)
	if err != nil {
		t.Fatalf("parse previewed new_period_end: %v", err)
	}
	if !previewedPeriodEnd.Equal(beforePeriodEnd) {
		t.Errorf("preview same-plan cycle switch: want new_period_end unchanged at %v, got %v — a regression to price-ratio proration would preview ~5 months further out than the real change would produce", beforePeriodEnd, previewedPeriodEnd)
	}
}

func TestIntegration_PreviewInvoice_Downgrade_IncludesOverage(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_preview_down_user"
		user2 = "integ_billing_preview_down_user2"
		wsID1 = "00000000-0000-0000-0000-000000000f34"
	)
	cleanupBillingByOrganization(pool, wsID1)
	t.Cleanup(func() {
		cleanupBillingByOrganization(pool, wsID1)
	})
	seedBillingOrganization(pool, wsID1, user)

	orgMod := organization.New(pool, messaging.NoopPublisher{})
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(orgMod)
	mod.SetOrganizationCommander(orgMod)

	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	// Upgrade to growth so we can downgrade
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: user})
		c.Next()
	}
	orgMW := func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{
			ID: wsID1, Slug: "test-ws", Name: "Test WS",
			Status: "active", OwnerID: user,
		})
		c.Set("organization.role", contracts.RoleOwner)
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	mod.Register(e.Group("/api"), httpserver.RouteDeps{Auth: authMW, RateLimit: noopMW, Org: orgMW, Idempotency: noopMW, RLS: noopMW, MFA: noopMW})

	// Add owner and second member to organization schema so ResolveDowngradeOverage finds them
	_, err := pool.Exec(t.Context(), `INSERT INTO organization.memberships (organization_id, auth_sub, role, joined_at) VALUES ($1, $2, 'owner', now()), ($1, $3, 'member', now())`, wsID1, user, user2)
	if err != nil {
		t.Fatalf("insert members: %v", err)
	}

	// Also record usage in billing schema so listCurrentUsage returns 2
	wUsage := httptest.NewRecorder()
	e.ServeHTTP(wUsage, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID1)+"/usage", `{"metric":"members","value":2}`))
	if wUsage.Code != http.StatusNoContent {
		t.Fatalf("record usage: %d", wUsage.Code)
	}

	// Manually change plan to growth to test downgrade
	wUpdate := httptest.NewRecorder()
	e.ServeHTTP(wUpdate, httpserver.JSONTestRequest(http.MethodPatch, billingURL(wsID1)+"/plan", `{"plan":"growth","cycle":"monthly","terms_agreed":true}`))
	if wUpdate.Code != http.StatusOK {
		t.Fatalf("upgrade: %d: %s", wUpdate.Code, wUpdate.Body)
	}

	// Preview downgrade to solo
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(wsID1)+"/preview?plan=solo&cycle=monthly", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("preview downgrade: %d: %s", w.Code, w.Body)
	}

	var resp struct {
		Data struct {
			Plan    string `json:"plan"`
			Overage *struct {
				Members struct {
					Current            int      `json:"current"`
					Allowed            int      `json:"allowed"`
					AutoSelectRemovals []string `json:"auto_select_removals"`
				} `json:"members"`
			} `json:"overage"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.Data.Plan != "solo" {
		t.Errorf("preview downgrade: want plan=solo, got %q", resp.Data.Plan)
	}
	if resp.Data.Overage == nil {
		t.Fatal("preview downgrade: want overage populated, got nil")
	}

	if resp.Data.Overage.Members.Current != 2 {
		t.Errorf("overage current members: want 2, got %d", resp.Data.Overage.Members.Current)
	}
	if resp.Data.Overage.Members.Allowed != 1 {
		t.Errorf("overage allowed members: want 1, got %d", resp.Data.Overage.Members.Allowed)
	}
	if len(resp.Data.Overage.Members.AutoSelectRemovals) != 1 {
		t.Errorf("overage autoselect members: want 1, got %d", len(resp.Data.Overage.Members.AutoSelectRemovals))
	}
}

// previewOverageResponse decodes GET .../billing/preview's data.overage shape,
// shared by the scheduled-overage preview tests below.
type previewOverageResponse struct {
	Data struct {
		Plan    string `json:"plan"`
		Overage *struct {
			Members struct {
				Current            int      `json:"current"`
				Allowed            int      `json:"allowed"`
				AutoSelectRemovals []string `json:"auto_select_removals"`
			} `json:"members"`
		} `json:"overage"`
	} `json:"data"`
}

// TestIntegration_PreviewInvoice_ScheduledNothingScheduled_OverageNil covers
// the common case for the new no-override branch: with nothing scheduled,
// wiring an OrganizationCommander must not populate Overage — same as
// before this branch existed.
func TestIntegration_PreviewInvoice_ScheduledNothingScheduled_OverageNil(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_preview_sched_none_user"
		wsID1 = "00000000-0000-0000-0000-00000000e011"
	)
	cleanupBillingByOrganization(pool, wsID1)
	t.Cleanup(func() { cleanupBillingByOrganization(pool, wsID1) })
	seedBillingOrganization(pool, wsID1, user)

	orgMod := organization.New(pool, messaging.NoopPublisher{})
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(orgMod)
	mod.SetOrganizationCommander(orgMod)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := gin.New()
	authMW := func(c *gin.Context) { c.Set("auth.claims", middleware.Claims{Subject: user}); c.Next() }
	orgMW := func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{ID: wsID1, Status: "active", OwnerID: user})
		c.Set("organization.role", contracts.RoleOwner)
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	mod.Register(e.Group("/api"), httpserver.RouteDeps{Auth: authMW, RateLimit: noopMW, Org: orgMW, Idempotency: noopMW, RLS: noopMW, MFA: noopMW})

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(wsID1)+"/preview", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("preview: want 200, got %d: %s", w.Code, w.Body)
	}
	var resp previewOverageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Overage != nil {
		t.Errorf("want overage nil with nothing scheduled, got %+v", *resp.Data.Overage)
	}
}

// TestIntegration_PreviewInvoice_ScheduledPlanDowngrade_OverLimit_OveragePopulated
// covers previewInvoice's new branch: called with no plan/cycle override on
// a subscription with a scheduled plan downgrade whose future limit current
// usage exceeds, Overage must be populated from a dry-run
// ResolveDowngradeOverage call against the scheduled plan's limits.
func TestIntegration_PreviewInvoice_ScheduledPlanDowngrade_OverLimit_OveragePopulated(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_preview_sched_plan_user"
		user2 = "integ_preview_sched_plan_user2"
		wsID1 = "00000000-0000-0000-0000-00000000e012"
	)
	cleanupBillingByOrganization(pool, wsID1)
	t.Cleanup(func() { cleanupBillingByOrganization(pool, wsID1) })
	seedBillingOrganization(pool, wsID1, user)

	orgMod := organization.New(pool, messaging.NoopPublisher{})
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(orgMod)
	mod.SetOrganizationCommander(orgMod)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, wsID1)
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET plan = 'growth' WHERE id = $1`, subID); err != nil {
		t.Fatalf("force growth: %v", err)
	}
	seedScheduledPlanDowngrade(t, pool, subID)

	if _, err := pool.Exec(t.Context(),
		`INSERT INTO organization.memberships (organization_id, auth_sub, role, joined_at) VALUES ($1, $2, 'owner', now()), ($1, $3, 'member', now())`,
		wsID1, user, user2); err != nil {
		t.Fatalf("insert members: %v", err)
	}
	seedMemberUsage(t, pool, wsID1, 2) // solo's scheduled limit is 1 — over

	e := gin.New()
	authMW := func(c *gin.Context) { c.Set("auth.claims", middleware.Claims{Subject: user}); c.Next() }
	orgMW := func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{ID: wsID1, Status: "active", OwnerID: user})
		c.Set("organization.role", contracts.RoleOwner)
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	mod.Register(e.Group("/api"), httpserver.RouteDeps{Auth: authMW, RateLimit: noopMW, Org: orgMW, Idempotency: noopMW, RLS: noopMW, MFA: noopMW})

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(wsID1)+"/preview", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("preview: want 200, got %d: %s", w.Code, w.Body)
	}
	var resp previewOverageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Plan != "growth" {
		t.Errorf("want the live plan reported (not the scheduled one), got %q", resp.Data.Plan)
	}
	if resp.Data.Overage == nil {
		t.Fatal("want overage populated for a scheduled downgrade over its future limit, got nil")
	}
	if resp.Data.Overage.Members.Current != 2 || resp.Data.Overage.Members.Allowed != 1 {
		t.Errorf("want current=2 allowed=1, got current=%d allowed=%d",
			resp.Data.Overage.Members.Current, resp.Data.Overage.Members.Allowed)
	}

	// A preview must never mutate state — still growth, still scheduled.
	var plan string
	var scheduledPlan *string
	pool.QueryRow(t.Context(), `SELECT plan, scheduled_plan FROM billing.subscriptions WHERE id = $1`, subID).Scan(&plan, &scheduledPlan)
	if plan != "growth" || scheduledPlan == nil || *scheduledPlan != "solo" {
		t.Errorf("preview must not mutate the subscription: want plan=growth scheduled_plan=solo, got plan=%q scheduled_plan=%v", plan, scheduledPlan)
	}
	var memberCount int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM organization.memberships WHERE organization_id = $1`, wsID1).Scan(&memberCount)
	if memberCount != 2 {
		t.Errorf("preview must not remove any member (dry-run only), got %d members left", memberCount)
	}
}

// TestIntegration_PreviewInvoice_ScheduledPlanAndAddonCombined_OveragePopulatedFromCombinedState
// mirrors the worker's own combined-computation test on the preview side: a
// scheduled plan downgrade *and* a scheduled addon decrease together must
// preview against the fully combined future state, not either one alone.
func TestIntegration_PreviewInvoice_ScheduledPlanAndAddonCombined_OveragePopulatedFromCombinedState(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_preview_sched_combo_user"
		user2 = "integ_preview_sched_combo_user2"
		user3 = "integ_preview_sched_combo_user3"
		wsID1 = "00000000-0000-0000-0000-00000000e013"
	)
	cleanupBillingByOrganization(pool, wsID1)
	t.Cleanup(func() { cleanupBillingByOrganization(pool, wsID1) })
	seedBillingOrganization(pool, wsID1, user)

	orgMod := organization.New(pool, messaging.NoopPublisher{})
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(orgMod)
	mod.SetOrganizationCommander(orgMod)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, wsID1)
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET plan = 'growth' WHERE id = $1`, subID); err != nil {
		t.Fatalf("force growth: %v", err)
	}
	seedScheduledPlanDowngrade(t, pool, subID)
	// solo (limit 1) + extra-seat scheduled down to x1 -> combined future limit 2.
	seedAddonWithScheduledQuantity(t, pool, subID, "extra-seat", 2, 1)

	if _, err := pool.Exec(t.Context(),
		`INSERT INTO organization.memberships (organization_id, auth_sub, role, joined_at) VALUES ($1, $2, 'owner', now()), ($1, $3, 'member', now()), ($1, $4, 'member', now())`,
		wsID1, user, user2, user3); err != nil {
		t.Fatalf("insert members: %v", err)
	}
	seedMemberUsage(t, pool, wsID1, 3) // over the combined future limit of 2

	e := gin.New()
	authMW := func(c *gin.Context) { c.Set("auth.claims", middleware.Claims{Subject: user}); c.Next() }
	orgMW := func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{ID: wsID1, Status: "active", OwnerID: user})
		c.Set("organization.role", contracts.RoleOwner)
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	mod.Register(e.Group("/api"), httpserver.RouteDeps{Auth: authMW, RateLimit: noopMW, Org: orgMW, Idempotency: noopMW, RLS: noopMW, MFA: noopMW})

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(wsID1)+"/preview", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("preview: want 200, got %d: %s", w.Code, w.Body)
	}
	var resp previewOverageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Overage == nil {
		t.Fatal("want overage populated for a combined scheduled plan+addon state over its limit, got nil")
	}
	if resp.Data.Overage.Members.Allowed != 2 {
		t.Errorf("want allowed=2 (combined future limit), got %d — a shallow implementation computing against only the plan or only the addon would show 1 or 3 instead",
			resp.Data.Overage.Members.Allowed)
	}

	var quantity int
	pool.QueryRow(t.Context(),
		`SELECT quantity FROM billing.subscription_addons WHERE subscription_id = $1 AND addon_id = 'extra-seat'`,
		subID).Scan(&quantity)
	if quantity != 2 {
		t.Errorf("preview must not apply the scheduled addon quantity change, want live quantity still 2, got %d", quantity)
	}
}

func TestIntegration_DowngradeSubscription_ManualSelection(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_down_man_user"
		user2 = "integ_billing_down_man_user2"
		wsID1 = "00000000-0000-0000-0000-000000000f35"
	)
	cleanupBillingByOrganization(pool, wsID1)
	t.Cleanup(func() { cleanupBillingByOrganization(pool, wsID1) })
	seedBillingOrganization(pool, wsID1, user)

	orgMod := organization.New(pool, messaging.NoopPublisher{})
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(orgMod)
	mod.SetOrganizationCommander(orgMod)

	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := gin.New()
	authMW := func(c *gin.Context) { c.Set("auth.claims", middleware.Claims{Subject: user}); c.Next() }
	orgMW := func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{ID: wsID1, Status: "active", OwnerID: user})
		c.Set("organization.role", contracts.RoleOwner)
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	mod.Register(e.Group("/api"), httpserver.RouteDeps{Auth: authMW, RateLimit: noopMW, Org: orgMW, Idempotency: noopMW, RLS: noopMW, MFA: noopMW})

	_, err := pool.Exec(t.Context(), `INSERT INTO organization.memberships (organization_id, auth_sub, role, joined_at) VALUES ($1, $2, 'owner', now()), ($1, $3, 'member', now())`, wsID1, user, user2)
	if err != nil {
		t.Fatalf("insert members: %v", err)
	}

	wUsage := httptest.NewRecorder()
	e.ServeHTTP(wUsage, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID1)+"/usage", `{"metric":"members","value":2}`))
	if wUsage.Code != http.StatusNoContent {
		t.Fatalf("record usage: %d", wUsage.Code)
	}

	wUpdate := httptest.NewRecorder()
	e.ServeHTTP(wUpdate, httpserver.JSONTestRequest(http.MethodPatch, billingURL(wsID1)+"/plan", `{"plan":"growth","cycle":"monthly","terms_agreed":true}`))
	if wUpdate.Code != http.StatusOK {
		t.Fatalf("upgrade: %d", wUpdate.Code)
	}

	wDown := httptest.NewRecorder()
	e.ServeHTTP(wDown, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID1)+"/downgrade", `{"plan":"solo","cycle":"monthly","preferred_member_auth_subs":["`+user2+`"]}`))
	if wDown.Code != http.StatusOK {
		t.Fatalf("downgrade: %d: %s", wDown.Code, wDown.Body)
	}

	// Verify member was actually removed
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM organization.memberships WHERE organization_id=$1`, wsID1).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("manual downgrade: want 1 member left, got %d", count)
	}

	// Verify the response body itself carries the overage summary — this is
	// what the frontend's success screen renders; the audit trail below is a
	// separate write of the same data, not the only place it should exist.
	var resp struct {
		Data struct {
			Overage contracts.OverageResolution `json:"overage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wDown.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal downgrade response: %v", err)
	}
	if !slices.Contains(resp.Data.Overage.RemovedMemberAuthSubs, user2) {
		t.Errorf("response overage: want %s in RemovedMemberAuthSubs, got %v", user2, resp.Data.Overage.RemovedMemberAuthSubs)
	}
	if slices.Contains(resp.Data.Overage.AutoSelectedMemberSubs, user2) {
		t.Errorf("response overage: %s was a manual pick, should not be in AutoSelectedMemberSubs, got %v", user2, resp.Data.Overage.AutoSelectedMemberSubs)
	}

	// Verify the audit trail: the downgrade's subscription_history row must
	// record user2 as removed and NOT as auto-selected, since it was the
	// owner's manual pick — this is the record support/the owner would
	// read after the fact to know what happened and why.
	var metadataRaw []byte
	if err := pool.QueryRow(t.Context(),
		`SELECT metadata FROM billing.subscription_history WHERE subscription_id=(SELECT id FROM billing.subscriptions WHERE subject_id=$1) AND action='downgrade' ORDER BY changed_at DESC LIMIT 1`,
		wsID1).Scan(&metadataRaw); err != nil {
		t.Fatalf("query subscription_history: %v", err)
	}
	var overage contracts.OverageResolution
	if err := json.Unmarshal(metadataRaw, &overage); err != nil {
		t.Fatalf("unmarshal subscription_history metadata: %v", err)
	}
	if !slices.Contains(overage.RemovedMemberAuthSubs, user2) {
		t.Errorf("subscription_history metadata: want %s in RemovedMemberAuthSubs, got %v", user2, overage.RemovedMemberAuthSubs)
	}
	if slices.Contains(overage.AutoSelectedMemberSubs, user2) {
		t.Errorf("subscription_history metadata: %s was a manual pick, should not be in AutoSelectedMemberSubs, got %v", user2, overage.AutoSelectedMemberSubs)
	}
}

func TestIntegration_DowngradeSubscription_AutoFill(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_down_auto_user"
		user2 = "integ_billing_down_auto_user2"
		wsID1 = "00000000-0000-0000-0000-000000000f36"
	)
	cleanupBillingByOrganization(pool, wsID1)
	t.Cleanup(func() { cleanupBillingByOrganization(pool, wsID1) })
	seedBillingOrganization(pool, wsID1, user)

	orgMod := organization.New(pool, messaging.NoopPublisher{})
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(orgMod)
	mod.SetOrganizationCommander(orgMod)

	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := gin.New()
	authMW := func(c *gin.Context) { c.Set("auth.claims", middleware.Claims{Subject: user}); c.Next() }
	orgMW := func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{ID: wsID1, Status: "active", OwnerID: user})
		c.Set("organization.role", contracts.RoleOwner)
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	mod.Register(e.Group("/api"), httpserver.RouteDeps{Auth: authMW, RateLimit: noopMW, Org: orgMW, Idempotency: noopMW, RLS: noopMW, MFA: noopMW})

	_, err := pool.Exec(t.Context(), `INSERT INTO organization.memberships (organization_id, auth_sub, role, joined_at) VALUES ($1, $2, 'owner', now()), ($1, $3, 'member', now())`, wsID1, user, user2)
	if err != nil {
		t.Fatalf("insert members: %v", err)
	}

	wUsage := httptest.NewRecorder()
	e.ServeHTTP(wUsage, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID1)+"/usage", `{"metric":"members","value":2}`))
	if wUsage.Code != http.StatusNoContent {
		t.Fatalf("record usage: %d", wUsage.Code)
	}

	wUpdate := httptest.NewRecorder()
	e.ServeHTTP(wUpdate, httpserver.JSONTestRequest(http.MethodPatch, billingURL(wsID1)+"/plan", `{"plan":"growth","cycle":"monthly","terms_agreed":true}`))
	if wUpdate.Code != http.StatusOK {
		t.Fatalf("upgrade: %d", wUpdate.Code)
	}

	wDown := httptest.NewRecorder()
	// No manual selections sent, it should trigger auto-fill
	e.ServeHTTP(wDown, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID1)+"/downgrade", `{"plan":"solo","cycle":"monthly"}`))
	if wDown.Code != http.StatusOK {
		t.Fatalf("downgrade: %d: %s", wDown.Code, wDown.Body)
	}

	// Verify member was actually removed
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM organization.memberships WHERE organization_id=$1`, wsID1).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("auto-fill downgrade: want 1 member left, got %d", count)
	}
}

func TestIntegration_DowngradeSubscription_ResumeOnRetry(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_down_retry_user"
		wsID1 = "00000000-0000-0000-0000-000000000f37"
	)
	cleanupBillingByOrganization(pool, wsID1)
	t.Cleanup(func() { cleanupBillingByOrganization(pool, wsID1) })
	seedBillingOrganization(pool, wsID1, user)

	orgMod := organization.New(pool, messaging.NoopPublisher{})
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(orgMod)
	mod.SetOrganizationCommander(orgMod)

	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := gin.New()
	authMW := func(c *gin.Context) { c.Set("auth.claims", middleware.Claims{Subject: user}); c.Next() }
	orgMW := func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{ID: wsID1, Status: "active", OwnerID: user})
		c.Set("organization.role", contracts.RoleOwner)
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	mod.Register(e.Group("/api"), httpserver.RouteDeps{Auth: authMW, RateLimit: noopMW, Org: orgMW, Idempotency: noopMW, RLS: noopMW, MFA: noopMW})

	// Already on solo
	wDown := httptest.NewRecorder()
	e.ServeHTTP(wDown, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID1)+"/downgrade", `{"plan":"solo","cycle":"monthly"}`))
	if wDown.Code != http.StatusOK {
		t.Fatalf("downgrade retry: %d: %s", wDown.Code, wDown.Body)
	}
}

// TestIntegration_DowngradeSubscription_ResumeOnRetry_AfterResolutionUnavailable
// is the genuine version of the risk the downgrade reordering was designed to
// mitigate — its sibling test above only exercises the trivial "already on
// target plan" branch by calling downgrade once when there was never
// anything to resolve. This test actually interrupts a real downgrade
// between the plan change and the overage resolution (by leaving
// orgCommander unset for the first call, simulating that dependency being
// unavailable — the same effect a real failure inside
// ResolveDowngradeOverage would have, since either way the method returns
// before resolving anything), confirms the org lands in the "benign
// grandfathered" state the ordering promised (cheaper plan, still
// over limit, nothing removed), then retries and confirms the resume
// branch actually finishes the job the first attempt couldn't.
func TestIntegration_DowngradeSubscription_ResumeOnRetry_AfterResolutionUnavailable(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_down_retry2_user"
		user2 = "integ_billing_down_retry2_user2"
		wsID1 = "00000000-0000-0000-0000-000000000f41"
	)
	cleanupBillingByOrganization(pool, wsID1)
	t.Cleanup(func() { cleanupBillingByOrganization(pool, wsID1) })
	seedBillingOrganization(pool, wsID1, user)

	orgMod := organization.New(pool, messaging.NoopPublisher{})
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(orgMod)
	// Deliberately not calling SetOrganizationCommander yet — the first
	// downgrade call below must go through with orgCommander nil.

	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := gin.New()
	authMW := func(c *gin.Context) { c.Set("auth.claims", middleware.Claims{Subject: user}); c.Next() }
	orgMW := func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{ID: wsID1, Status: "active", OwnerID: user})
		c.Set("organization.role", contracts.RoleOwner)
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	mod.Register(e.Group("/api"), httpserver.RouteDeps{Auth: authMW, RateLimit: noopMW, Org: orgMW, Idempotency: noopMW, RLS: noopMW, MFA: noopMW})

	if _, err := pool.Exec(t.Context(),
		`INSERT INTO organization.memberships (organization_id, auth_sub, role, joined_at) VALUES ($1, $2, 'owner', now()), ($1, $3, 'member', now())`,
		wsID1, user, user2); err != nil {
		t.Fatalf("insert members: %v", err)
	}
	wUsage := httptest.NewRecorder()
	e.ServeHTTP(wUsage, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID1)+"/usage", `{"metric":"members","value":2}`))
	if wUsage.Code != http.StatusNoContent {
		t.Fatalf("record usage: %d", wUsage.Code)
	}

	wUpdate := httptest.NewRecorder()
	e.ServeHTTP(wUpdate, httpserver.JSONTestRequest(http.MethodPatch, billingURL(wsID1)+"/plan", `{"plan":"growth","cycle":"monthly","terms_agreed":true}`))
	if wUpdate.Code != http.StatusOK {
		t.Fatalf("upgrade: %d", wUpdate.Code)
	}

	// First attempt: orgCommander is nil, so the plan change goes through
	// but overage resolution can't run — this is the interrupted state.
	wDown1 := httptest.NewRecorder()
	e.ServeHTTP(wDown1, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID1)+"/downgrade", `{"plan":"solo","cycle":"monthly"}`))
	if wDown1.Code != http.StatusOK {
		t.Fatalf("first downgrade attempt: %d: %s", wDown1.Code, wDown1.Body)
	}

	var planAfterFirst string
	if err := pool.QueryRow(t.Context(), `SELECT plan FROM billing.subscriptions WHERE subject_id=$1`, wsID1).Scan(&planAfterFirst); err != nil {
		t.Fatal(err)
	}
	if planAfterFirst != "solo" {
		t.Fatalf("plan change should have gone through despite resolution being unavailable: got %q, want solo", planAfterFirst)
	}
	var countAfterFirst int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM organization.memberships WHERE organization_id=$1`, wsID1).Scan(&countAfterFirst); err != nil {
		t.Fatal(err)
	}
	if countAfterFirst != 2 {
		t.Fatalf("member should NOT have been removed yet (resolution was unavailable): got %d members, want 2", countAfterFirst)
	}

	// "Recovery": the dependency the first attempt was missing is now
	// available. Retry the identical request.
	mod.SetOrganizationCommander(orgMod)

	wDown2 := httptest.NewRecorder()
	e.ServeHTTP(wDown2, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID1)+"/downgrade", `{"plan":"solo","cycle":"monthly"}`))
	if wDown2.Code != http.StatusOK {
		t.Fatalf("retry downgrade: %d: %s", wDown2.Code, wDown2.Body)
	}

	var countAfterRetry int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM organization.memberships WHERE organization_id=$1`, wsID1).Scan(&countAfterRetry); err != nil {
		t.Fatal(err)
	}
	if countAfterRetry != 1 {
		t.Errorf("retry should have finished resolving the overage the first attempt couldn't: got %d members, want 1", countAfterRetry)
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

func TestIntegration_Webhook_Extension_Concurrency(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_concur"
		wsID0 = "00000000-0000-0000-0000-000000000f59"
		wsID1 = "00000000-0000-0000-0000-000000000f60"
	)
	cleanupBillingByOrganization(pool, wsID0)
	cleanupBillingByOrganization(pool, wsID1)
	// This test's whole point is 10 distinct event IDs landing concurrently,
	// so (unlike every other webhook test here) it can't just omit the event
	// ID to dodge billing.webhook_events' cross-run idempotency marker.
	// cleanupBillingByOrganization can't reach these rows either (it only
	// prunes event_ids prefixed by this org's own payment-link external_id)
	// so they'd otherwise survive forever and silently no-op every rerun via
	// markWebhookProcessed's ON CONFLICT DO NOTHING — clean them up directly.
	pool.Exec(t.Context(), `DELETE FROM billing.webhook_events WHERE provider = 'stripe' AND event_id LIKE 'evt_concur_%'`)
	t.Cleanup(func() {
		cleanupBillingByOrganization(pool, wsID0)
		cleanupBillingByOrganization(pool, wsID1)
		pool.Exec(context.Background(), `DELETE FROM billing.webhook_events WHERE provider = 'stripe' AND event_id LIKE 'evt_concur_%'`)
	})
	seedBillingOrganization(pool, wsID0, user)
	seedBillingOrganization(pool, wsID1, user)

	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(organization.New(pool, messaging.NoopPublisher{}))
	// wsID0 takes this user's one-per-user trial; wsID1 (this test's subject)
	// provisions straight to "active" — extension only applies to active
	// subscriptions, same pattern as TestIntegration_ExtendSubscription_HappyPath.
	_ = mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID0, user))
	_ = mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user))
	payProvisioningInvoice(pool, wsID1)

	e := billing.NewWebhookModuleEngine(pool, user, wsID1)
	w1 := httptest.NewRecorder()
	e.ServeHTTP(w1, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID1)+"/extend", `{"months":1}`))
	var resp1 struct {
		Data struct {
			ID          string `json:"id"`
			AmountCents int64  `json:"amount_cents"`
		} `json:"data"`
	}
	json.NewDecoder(w1.Body).Decode(&resp1)

	const extID = "stripe_sess_concur"
	seedPaymentLink(pool, resp1.Data.ID, extID, "stripe", "USD", resp1.Data.AmountCents)

	// blast the webhook concurrently — same invoice, 10 distinct event IDs,
	// simulating redelivery under different event IDs (e.g. a provider
	// retry, or two distinct event types reporting the same underlying
	// payment) rather than the same event ID twice, which the eventID-based
	// marker in processWebhook would already dedupe trivially. This exercises
	// markInvoicePaid's own pending->paid guard (service_webhook.go) instead.
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := fmt.Sprintf(`{"id":"evt_concur_%d","type":"checkout.session.completed","data":{"object":{"id":%q,"payment_status":"paid"}}}`, i, extID)
			wWeb := httptest.NewRecorder()
			e.ServeHTTP(wWeb, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe", payload))
		}(i)
	}
	wg.Wait()

	var historyCount int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.subscription_history WHERE subscription_id = (SELECT id FROM billing.subscriptions WHERE subject_id = $1) AND action = 'extend'`, wsID1,
	).Scan(&historyCount)
	if historyCount != 1 {
		t.Errorf("want exactly 1 'extend' history row despite concurrent webhook deliveries, got %d", historyCount)
	}
}

func TestIntegration_DowngradeSubscription_AddonLimit(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_down_addon_user"
		user2 = "integ_billing_down_addon_user2"
		user3 = "integ_billing_down_addon_user3"
		user4 = "integ_billing_down_addon_user4"
		wsID1 = "00000000-0000-0000-0000-000000000f38"
	)
	cleanupBillingByOrganization(pool, wsID1)
	t.Cleanup(func() { cleanupBillingByOrganization(pool, wsID1) })
	seedBillingOrganization(pool, wsID1, user)

	orgMod := organization.New(pool, messaging.NoopPublisher{})
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(orgMod)
	mod.SetOrganizationCommander(orgMod)

	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	subID := getSubscriptionID(pool, wsID1)

	e := gin.New()
	authMW := func(c *gin.Context) { c.Set("auth.claims", middleware.Claims{Subject: user}); c.Next() }
	orgMW := func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{ID: wsID1, Status: "active", OwnerID: user})
		c.Set("organization.role", contracts.RoleOwner)
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	mod.Register(e.Group("/api"), httpserver.RouteDeps{Auth: authMW, RateLimit: noopMW, Org: orgMW, Idempotency: noopMW, RLS: noopMW, MFA: noopMW})

	// Upgrade to growth first
	wUpdate := httptest.NewRecorder()
	e.ServeHTTP(wUpdate, httpserver.JSONTestRequest(http.MethodPatch, billingURL(wsID1)+"/plan", `{"plan":"growth","cycle":"monthly","terms_agreed":true}`))
	if wUpdate.Code != http.StatusOK {
		t.Fatalf("upgrade: %d", wUpdate.Code)
	}

	// Add 3 more members, so 4 total. (Growth allows 15, so this is well within limit).
	_, err := pool.Exec(t.Context(), `INSERT INTO organization.memberships (organization_id, auth_sub, role, joined_at) VALUES ($1, $2, 'member', now()), ($1, $3, 'member', now()), ($1, $4, 'member', now())`, wsID1, user2, user3, user4)
	if err != nil {
		t.Fatalf("insert members: %v", err)
	}

	// Record usage = 4 members
	wUsage := httptest.NewRecorder()
	e.ServeHTTP(wUsage, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID1)+"/usage", `{"metric":"members","value":4}`))
	if wUsage.Code != http.StatusNoContent {
		t.Fatalf("record usage: %d", wUsage.Code)
	}

	// Attach addon +2 extra seats
	_, err = pool.Exec(t.Context(), `INSERT INTO billing.subscription_addons (subscription_id, addon_id, quantity) VALUES ($1, 'extra-seat', 2)`, subID)
	if err != nil {
		t.Fatalf("attach addon: %v", err)
	}

	// Preview downgrade to solo
	wPrev := httptest.NewRecorder()
	e.ServeHTTP(wPrev, httpserver.JSONTestRequest(http.MethodGet, billingURL(wsID1)+"/preview?plan=solo&cycle=monthly", ""))
	if wPrev.Code != http.StatusOK {
		t.Fatalf("preview downgrade: %d: %s", wPrev.Code, wPrev.Body)
	}
	var prevResp struct {
		Data struct {
			Overage struct {
				Members struct {
					Current int `json:"current"`
					Allowed int `json:"allowed"`
				} `json:"members"`
			} `json:"overage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wPrev.Body.Bytes(), &prevResp); err != nil {
		t.Fatalf("parse preview: %v", err)
	}

	// solo base member limit is 1. +2 addon = 3.
	// We have 4 members, so allowed should be 3.
	if prevResp.Data.Overage.Members.Allowed != 3 {
		t.Errorf("preview allowed members: want 3, got %d", prevResp.Data.Overage.Members.Allowed)
	}

	wDown := httptest.NewRecorder()
	// No manual selections sent, it should trigger auto-fill, removing 4 - 3 = 1 member
	e.ServeHTTP(wDown, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID1)+"/downgrade", `{"plan":"solo","cycle":"monthly"}`))
	if wDown.Code != http.StatusOK {
		t.Fatalf("downgrade: %d: %s", wDown.Code, wDown.Body)
	}

	// Verify members left
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM organization.memberships WHERE organization_id=$1`, wsID1).Scan(&count); err != nil {
		t.Fatal(err)
	}
	// solo limit (1) + addon (2) = 3 members allowed
	if count != 3 {
		t.Errorf("auto-fill downgrade with addon: want 3 members left, got %d", count)
	}
}

// TestIntegration_DowngradeSubscription_RealRLS_ActuallyRemovesMember uses
// the real middleware.NewRLSTxMiddleware, not the noopMW every other test in
// this file substitutes for it — that substitution is exactly why the
// prod-only crash this test guards against went undetected: NewRLSTxMiddleware
// is what stashes a request-scoped *pgx.Tx into the request context
// (db.WithQuerier), and only a downgrade that actually removes a member
// exercises the organization module's fire-and-forget syncMemberUsage, which
// calls back into billing.RecordUsage via a context.WithoutCancel-derived
// context. Before the WithoutQuerier fix, that context still carried the
// caller's in-flight transaction, so the background goroutine and the
// request's own goroutine both drove the same *pgx.Conn concurrently —
// observed live as "fatal error: concurrent map writes" in pgx's internal
// statement cache. -race (part of this suite's standard run) is the actual
// regression guard; this test's job is just to exercise the real code path
// that a fatal error can't be recovered from or asserted on directly.
func TestIntegration_DowngradeSubscription_RealRLS_ActuallyRemovesMember(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_down_rls_user"
		user2 = "integ_billing_down_rls_user2"
		wsID1 = "00000000-0000-0000-0000-000000000f39"
	)
	cleanupBillingByOrganization(pool, wsID1)
	t.Cleanup(func() { cleanupBillingByOrganization(pool, wsID1) })
	seedBillingOrganization(pool, wsID1, user)

	orgMod := organization.New(pool, messaging.NoopPublisher{})
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(orgMod)
	mod.SetOrganizationCommander(orgMod)
	orgMod.SetBillingReader(mod)
	orgMod.SetBillingWriter(mod)

	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := gin.New()
	authMW := func(c *gin.Context) { c.Set("auth.claims", middleware.Claims{Subject: user}); c.Next() }
	orgMW := func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{ID: wsID1, Status: "active", OwnerID: user})
		c.Set("organization.role", contracts.RoleOwner)
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	mod.Register(e.Group("/api"), httpserver.RouteDeps{
		Auth: authMW, RateLimit: noopMW, Org: orgMW, Idempotency: noopMW,
		RLS: middleware.NewRLSTxMiddleware(pool), MFA: noopMW,
	})

	// The owner's own membership row — resolveDowngradeOverage's "never
	// remove the owner" exclusion only has something to exclude if this
	// exists; HandleOrganizationCreated above only provisions the billing
	// subscription, not the organization-side membership row a real
	// POST /organizations would create.
	if _, err := pool.Exec(t.Context(), `INSERT INTO organization.memberships (organization_id, auth_sub, role, joined_at) VALUES ($1, $2, 'owner', now())`, wsID1, user); err != nil {
		t.Fatalf("insert owner membership: %v", err)
	}

	// Repeat the upgrade→add-member→downgrade-and-remove cycle several times
	// in one process: a data race is timing-dependent and a single
	// iteration isn't guaranteed to hit the exact interleaving even when
	// the bug is present, so more attempts (under -race's happens-before
	// instrumentation, which persists across iterations within one process)
	// give it more chances to be caught if this ever regresses.
	for i := range 15 {
		wUpdate := httptest.NewRecorder()
		e.ServeHTTP(wUpdate, httpserver.JSONTestRequest(http.MethodPatch, billingURL(wsID1)+"/plan", `{"plan":"growth","cycle":"monthly","terms_agreed":true}`))
		if wUpdate.Code != http.StatusOK {
			t.Fatalf("iteration %d: upgrade: %d", i, wUpdate.Code)
		}

		_, err := pool.Exec(t.Context(), `INSERT INTO organization.memberships (organization_id, auth_sub, role, joined_at) VALUES ($1, $2, 'member', now())`, wsID1, user2)
		if err != nil {
			t.Fatalf("iteration %d: insert member: %v", i, err)
		}
		wUsage := httptest.NewRecorder()
		e.ServeHTTP(wUsage, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID1)+"/usage", `{"metric":"members","value":2}`))
		if wUsage.Code != http.StatusNoContent {
			t.Fatalf("iteration %d: record usage: %d", i, wUsage.Code)
		}

		// Auto-fill downgrade to solo (limit 1): the owner and user2 are
		// both present, so this must actually remove user2, which is what
		// fires syncMemberUsage's fire-and-forget call back into
		// billing.RecordUsage through the real request-scoped transaction
		// this test's RLS middleware opened.
		wDown := httptest.NewRecorder()
		e.ServeHTTP(wDown, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID1)+"/downgrade", `{"plan":"solo","cycle":"monthly"}`))
		if wDown.Code != http.StatusOK {
			t.Fatalf("iteration %d: downgrade: %d: %s", i, wDown.Code, wDown.Body)
		}

		var count int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM organization.memberships WHERE organization_id=$1`, wsID1).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("iteration %d: want 1 member left (owner only), got %d", i, count)
		}

		// The background usage sync races the test's own assertions by
		// design (that's the point) — poll briefly rather than asserting
		// immediately, so this doesn't flake on a slow CI runner.
		deadline := time.Now().Add(2 * time.Second)
		for {
			var usage int64
			uErr := pool.QueryRow(t.Context(),
				`SELECT value FROM billing.usage WHERE organization_id=$1 AND metric='members'`, wsID1).Scan(&usage)
			if uErr == nil && usage == 1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("iteration %d: usage sync after downgrade never converged to 1 (last err=%v, value=%d)", i, uErr, usage)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// TestIntegration_DowngradeSubscription_ConcurrentDoubleSubmit verifies the
// per-organization lock requirement (lockSubscriptionForUpdate's
// SELECT ... FOR UPDATE, held for the real RLS transaction's lifetime):
// two concurrent downgrade requests for the same org must serialize, not
// double-remove members or otherwise race. Uses the real RLS middleware
// (see TestIntegration_DowngradeSubscription_RealRLS_ActuallyRemovesMember's
// comment for why every other test in this file substituting noopMW matters
// here too — a noop RLS means no real transaction, and no real transaction
// means the row lock is a no-op).
func TestIntegration_DowngradeSubscription_ConcurrentDoubleSubmit(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_billing_down_concurrent_user"
		user2 = "integ_billing_down_concurrent_user2"
		user3 = "integ_billing_down_concurrent_user3"
		wsID1 = "00000000-0000-0000-0000-000000000f40"
	)
	cleanupBillingByOrganization(pool, wsID1)
	t.Cleanup(func() { cleanupBillingByOrganization(pool, wsID1) })
	seedBillingOrganization(pool, wsID1, user)

	orgMod := organization.New(pool, messaging.NoopPublisher{})
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(orgMod)
	mod.SetOrganizationCommander(orgMod)
	orgMod.SetBillingReader(mod)
	orgMod.SetBillingWriter(mod)

	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(wsID1, user)); err != nil {
		t.Fatalf("provision: %v", err)
	}

	e := gin.New()
	authMW := func(c *gin.Context) { c.Set("auth.claims", middleware.Claims{Subject: user}); c.Next() }
	orgMW := func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{ID: wsID1, Status: "active", OwnerID: user})
		c.Set("organization.role", contracts.RoleOwner)
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	mod.Register(e.Group("/api"), httpserver.RouteDeps{
		Auth: authMW, RateLimit: noopMW, Org: orgMW, Idempotency: noopMW,
		RLS: middleware.NewRLSTxMiddleware(pool), MFA: noopMW,
	})

	if _, err := pool.Exec(t.Context(),
		`INSERT INTO organization.memberships (organization_id, auth_sub, role, joined_at) VALUES
		 ($1, $2, 'owner', now()), ($1, $3, 'member', now()), ($1, $4, 'member', now())`,
		wsID1, user, user2, user3); err != nil {
		t.Fatalf("insert members: %v", err)
	}

	wUpdate := httptest.NewRecorder()
	e.ServeHTTP(wUpdate, httpserver.JSONTestRequest(http.MethodPatch, billingURL(wsID1)+"/plan", `{"plan":"growth","cycle":"monthly","terms_agreed":true}`))
	if wUpdate.Code != http.StatusOK {
		t.Fatalf("upgrade: %d", wUpdate.Code)
	}

	// Fire two identical auto-fill downgrade requests for the same org at
	// once. Whichever acquires the row lock first changes the plan and
	// removes both extra members; the other blocks on the lock, then (once
	// it proceeds) finds the subscription already on the target plan and
	// takes the resume-on-retry branch — both must return 200, and members
	// must be removed exactly once, not twice or zero times.
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(wsID1)+"/downgrade", `{"plan":"solo","cycle":"monthly"}`))
			codes[i] = w.Code
		}(i)
	}
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("concurrent request %d: status = %d, want 200", i, code)
		}
	}

	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM organization.memberships WHERE organization_id=$1`, wsID1).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("want exactly 1 member left (owner only) after both requests settle, got %d — a race would show up as 0 (double-removed, second delete matching nothing is harmless) or the members never actually removed", count)
	}

	var plan string
	if err := pool.QueryRow(t.Context(), `SELECT plan FROM billing.subscriptions WHERE subject_id=$1`, wsID1).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if plan != "solo" {
		t.Errorf("want plan solo after both requests settle, got %q", plan)
	}
}
