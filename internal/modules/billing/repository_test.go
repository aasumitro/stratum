package billing_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

const testAuthSubBilling = "integ_sub_billing_1"

// testPoolBilling seeds billing.subscriptions directly and several integration tests in this
// package create throwaway trigger functions to inject a mid-transaction failure — FORCE ROW LEVEL
// SECURITY needs BYPASSRLS for the former, CREATE on billing/public needs schema ownership for the
// latter. TEST_DATABASE_URL (stratum_test, see deploy/postgres-init/02-test-role.sql) holds both,
// by design rather than by container-superuser accident — see docs/09-testing.md.
func testPoolBilling(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// encodeOrganizationCreatedEvent builds a minimal OrganizationCreated event
// body. Plan/Cycle are required at the API boundary (see
// createOrganizationRequest) and cycle is DB CHECK-constrained to
// 'monthly'/'yearly' — an empty cycle here would fail the INSERT before
// provisionSubscription even reaches catalog/invoice logic, so both are set
// even though these callers pass catalog=nil and never exercise pricing.
func encodeOrganizationCreatedEvent(organizationID string) []byte {
	env := events.Envelope{
		ID:     "test-event-id",
		Type:   events.RoutingKeyOrganizationCreated,
		Source: "organization",
		Time:   time.Now(),
		OrgID:  organizationID,
		Data: events.OrganizationCreated{
			OrganizationID: organizationID,
			Slug:           "test-billing-ws",
			Name:           "Test Billing WS",
			CreatedBy:      testAuthSubBilling,
			Plan:           "solo",
			Cycle:          "monthly",
			CreatedAt:      time.Now(),
		},
	}
	body, _ := json.Marshal(env)
	return body
}

func cleanupBillingByOrganization(pool *pgxpool.Pool, orgID string) {
	ctx := context.Background()
	// Every events.Enqueue call in the code under test writes a real row
	// here now — tests using a fixed orgID literal (most of this file's
	// helpers do) would otherwise see a previous run's leftover outbox rows
	// bleed into an "outbox is empty for this org" assertion. Two key names
	// because billing.* events envelope the org as "org_id" while
	// organization.* events (e.g. organization.created, which this org's
	// own provisioning flow publishes) use "organization_id" — matching only
	// one left every organization-keyed row permanently unswept.
	pool.Exec(ctx, `DELETE FROM messaging.outbox WHERE payload->>'org_id' = $1 OR payload->>'organization_id' = $1`, orgID)
	pool.Exec(ctx, `DELETE FROM billing.usage WHERE organization_id = $1`, orgID)
	pool.Exec(ctx, `DELETE FROM billing.invoice_sequences WHERE organization_id = $1`, orgID)
	// webhook_events first — keyed by payment_link external_id, must run before payment_links are deleted
	pool.Exec(ctx, `
		DELETE FROM billing.webhook_events we
		WHERE EXISTS (
			SELECT 1 FROM billing.payment_links pl
			JOIN billing.invoices i ON i.id = pl.invoice_id
			JOIN billing.subscriptions s ON s.id = i.subscription_id
			WHERE s.subject_type = 'organization' AND s.subject_id = $1
			  AND we.event_id LIKE pl.external_id || '%'
		)`, orgID)
	pool.Exec(ctx, `DELETE FROM billing.payments WHERE invoice_id IN (SELECT i.id FROM billing.invoices i JOIN billing.subscriptions s ON s.id = i.subscription_id WHERE s.subject_type = 'organization' AND s.subject_id = $1)`, orgID)
	pool.Exec(ctx, `DELETE FROM billing.payment_links WHERE invoice_id IN (SELECT i.id FROM billing.invoices i JOIN billing.subscriptions s ON s.id = i.subscription_id WHERE s.subject_type = 'organization' AND s.subject_id = $1)`, orgID)
	pool.Exec(ctx, `DELETE FROM billing.subscription_history WHERE subscription_id IN (SELECT id FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1)`, orgID)
	pool.Exec(ctx, `DELETE FROM billing.subscription_addons WHERE subscription_id IN (SELECT id FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1)`, orgID)
	pool.Exec(ctx, `DELETE FROM billing.coupon_redemptions WHERE subscription_id IN (SELECT id FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1)`, orgID)
	pool.Exec(ctx, `DELETE FROM billing.invoice_line_items WHERE invoice_id IN (SELECT i.id FROM billing.invoices i JOIN billing.subscriptions s ON s.id = i.subscription_id WHERE s.subject_type = 'organization' AND s.subject_id = $1)`, orgID)
	pool.Exec(ctx, `DELETE FROM billing.invoices WHERE subscription_id IN (SELECT id FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1)`, orgID)
	pool.Exec(ctx, `DELETE FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1`, orgID)
	pool.Exec(ctx, `DELETE FROM organization.organizations WHERE id = $1`, orgID)
}

// setupBillingTest pre-cleans any dirty state from previous runs and registers
// cleanup for after the test. Call at the top of every billing integration test.
func setupBillingTest(t *testing.T, pool *pgxpool.Pool, orgID string) {
	t.Helper()
	cleanupBillingByOrganization(pool, orgID)
	t.Cleanup(func() { cleanupBillingByOrganization(pool, orgID) })
}

// testAddonID is a second, independently-tracked addon several amendment
// tests need to prove per-addon bookkeeping doesn't cross-contaminate — the
// real catalog only has one addon (extra-seat). Mapped to the "workspaces"
// feature (metered, unrelated to members) rather than a made-up feature, so
// tests asserting independent per-metric deltas still exercise a real,
// distinct metric.
const testAddonID = "extra-workspace"

// seedTestAddonCatalogRow inserts testAddonID into the billing catalog for
// tests that need a second real addon ID, and cleans it up afterward. Must
// be called before setupBillingTest/seedActiveOrgNoSchedule so t.Cleanup's
// LIFO order runs the subscription/subscription_addons cleanup first,
// clearing the FK reference before this row is deleted.
func seedTestAddonCatalogRow(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `
		INSERT INTO billing.addons (id, name, description, prices) VALUES
		($1, 'Test +1 Workspace', 'Test-only addon for integration tests.',
		 '{"USD": {"monthly": 100, "yearly": 1000}, "IDR": {"monthly": 10000, "yearly": 100000}}')
		ON CONFLICT (id) DO NOTHING`, testAddonID); err != nil {
		t.Fatalf("seed test addon catalog row: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO billing.addon_features (addon_id, feature_id, limit_value) VALUES ($1, 'workspaces', 1)
		ON CONFLICT (addon_id, feature_id) DO NOTHING`, testAddonID); err != nil {
		t.Fatalf("seed test addon_features row: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM billing.addon_features WHERE addon_id = $1`, testAddonID)
		pool.Exec(context.Background(), `DELETE FROM billing.addons WHERE id = $1`, testAddonID)
	})
}

// seedInvoice inserts a pending invoice for a subscription and returns its ID.
func seedInvoice(pool *pgxpool.Pool, subscriptionID, currency string, amountCents int64) string {
	var id string
	pool.QueryRow(context.Background(), `
		INSERT INTO billing.invoices (subscription_id, amount_cents, currency)
		VALUES ($1, $2, $3) RETURNING id`,
		subscriptionID, amountCents, currency,
	).Scan(&id)
	return id
}

// couponSeedOpts configures seedCoupon; zero-value fields fall back to
// sensible defaults (cadence="once", active=true).
type couponSeedOpts struct {
	DiscountType   string
	AmountCents    *int64
	PercentOff     *int16
	Cadence        string
	DurationCount  *int
	ValidFrom      *time.Time
	ValidUntil     *time.Time
	MaxRedemptions *int
	RedeemedCount  int
	Active         *bool
}

// seedCoupon inserts a billing.coupons row for code and registers its own
// cleanup — coupons/coupon_targets/coupon_redemptions aren't organization-
// scoped like the rest of this file's fixtures, so they need separate
// teardown from cleanupBillingByOrganization. Relies on t.Cleanup's LIFO
// order: call this after setupBillingTest so the coupon (and any
// redemptions referencing the test subscription) are torn down before the
// subscription itself is.
func seedCoupon(t *testing.T, pool *pgxpool.Pool, code string, opts couponSeedOpts) {
	t.Helper()
	cadence := opts.Cadence
	if cadence == "" {
		cadence = "once"
	}
	active := true
	if opts.Active != nil {
		active = *opts.Active
	}
	_, err := pool.Exec(t.Context(), `
		INSERT INTO billing.coupons (code, name, discount_type, amount_cents, percent_off, cadence, duration_count, valid_from, valid_until, max_redemptions, redeemed_count, active)
		VALUES ($1, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		code, opts.DiscountType, opts.AmountCents, opts.PercentOff, cadence, opts.DurationCount,
		opts.ValidFrom, opts.ValidUntil, opts.MaxRedemptions, opts.RedeemedCount, active,
	)
	if err != nil {
		t.Fatalf("seedCoupon %s: %v", code, err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM billing.coupon_targets WHERE coupon_id = $1`, code)
		pool.Exec(ctx, `DELETE FROM billing.coupon_redemptions WHERE coupon_id = $1`, code)
		pool.Exec(ctx, `DELETE FROM billing.coupons WHERE code = $1`, code)
	})
}

// seedPaidInvoice inserts an already-paid invoice for a subscription and returns its ID.
func seedPaidInvoice(pool *pgxpool.Pool, subscriptionID, currency string, amountCents int64) string {
	var id string
	pool.QueryRow(context.Background(), `
		INSERT INTO billing.invoices (subscription_id, amount_cents, currency, status)
		VALUES ($1, $2, $3, 'paid') RETURNING id`,
		subscriptionID, amountCents, currency,
	).Scan(&id)
	return id
}

// seedPaymentLink inserts a pending payment link with a known externalID and returns its ID.
func seedPaymentLink(pool *pgxpool.Pool, invoiceID, externalID, provider, currency string, amountCents int64) string {
	var id string
	pool.QueryRow(context.Background(), `
		INSERT INTO billing.payment_links (invoice_id, provider, currency, amount_cents, external_id, url)
		VALUES ($1, $2, $3, $4, $5, 'https://pay.example.com') RETURNING id`,
		invoiceID, provider, currency, amountCents, externalID,
	).Scan(&id)
	return id
}

// getSubscriptionID returns the subscription ID for an organization subject.
func getSubscriptionID(pool *pgxpool.Pool, orgID string) string {
	var id string
	pool.QueryRow(context.Background(),
		`SELECT id FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1`, orgID,
	).Scan(&id)
	return id
}

// forceHistoryInsertFailure installs a trigger that raises an error on any
// INSERT into billing.subscription_history for subscriptionID, so a caller
// can prove a preceding write (e.g. updateSubscriptionStatus) rolls back
// when insertHistory fails inside the same transaction. CREATE TRIGGER
// doesn't support query parameters, so subscriptionID (already validated as
// a UUID) is inlined directly rather than passed as a bind argument.
func forceHistoryInsertFailure(t *testing.T, pool *pgxpool.Pool, subscriptionID string) {
	t.Helper()
	if _, err := uuid.Parse(subscriptionID); err != nil {
		t.Fatalf("forceHistoryInsertFailure: not a UUID: %v", err)
	}
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION test_force_history_insert_failure() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'test-induced history insert failure';
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create trigger function: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE TRIGGER trg_test_force_history_insert_failure
		BEFORE INSERT ON billing.subscription_history
		FOR EACH ROW WHEN (NEW.subscription_id = '`+subscriptionID+`')
		EXECUTE FUNCTION test_force_history_insert_failure()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(),
			`DROP TRIGGER IF EXISTS trg_test_force_history_insert_failure ON billing.subscription_history`)
		pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS test_force_history_insert_failure()`)
	})
}

// getSubscriptionExpectedEnd returns the subscription's current trial_end (if
// trialing) or period_end — the value HandleSubscriptionRemind/AutoInvoice's
// staleness guard compares a delayed SubscriptionCheck's ExpectedEnd against.
// Tests simulating a delayed check firing "on time" must pass this, not an
// arbitrary offset, or the guard treats the check as stale and no-ops.
func getSubscriptionExpectedEnd(pool *pgxpool.Pool, subID string) (end time.Time, isTrial bool) {
	var periodEnd, trialEnd *time.Time
	pool.QueryRow(context.Background(),
		`SELECT period_end, trial_end FROM billing.subscriptions WHERE id = $1`, subID,
	).Scan(&periodEnd, &trialEnd)
	if trialEnd != nil {
		return *trialEnd, true
	}
	return *periodEnd, false
}

// seedBillingOrganization inserts a minimal organization.organizations row so that
// countSubscriptionsByOwner (which JOINs organization.organizations) can resolve the owner.
func seedBillingOrganization(pool *pgxpool.Pool, orgID, ownerID string) {
	pool.Exec(context.Background(), `
		INSERT INTO organization.organizations (id, slug, name, owner_id, status)
		VALUES ($1, $2, $2, $3, 'active')
		ON CONFLICT DO NOTHING`,
		orgID, "billing-test-"+orgID[len(orgID)-4:], ownerID)
}

// payProvisioningInvoice marks a freshly-provisioned subscription's own
// pending invoice paid directly (not via a webhook round trip) so
// extendSubscription's pending-invoice guard no longer sees it. Safe as a
// no-op setup step for tests exercising Extend afterward: the provisioning
// invoice's kind is "activation", whose payment never mutates period_end
// (see handleWebhook's kind switch) — only the webhook path applies period
// side effects, and this bypasses that path entirely.
func payProvisioningInvoice(pool *pgxpool.Pool, orgID string) {
	pool.Exec(context.Background(), `
		UPDATE billing.invoices SET status = 'paid', paid_at = now()
		WHERE status = 'pending' AND subscription_id = (
			SELECT id FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1
		)`, orgID)
}

func TestIntegration_ProvisionAndGetSubscription(t *testing.T) {
	pool := testPoolBilling(t)

	const orgID = "00000000-0000-0000-0000-000000000b01"
	t.Cleanup(func() { cleanupBillingByOrganization(pool, orgID) })

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEvent(orgID)); err != nil {
		t.Fatalf("provision subscription: %v", err)
	}

	// verify via HTTP
	e := billing.NewModuleEngine(pool, testAuthSubBilling, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/api/organizations/"+orgID+"/billing", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data := resp["data"].(map[string]any)
	if data["plan"] != "solo" {
		t.Errorf("want plan solo, got %v", data["plan"])
	}
}

func TestIntegration_ListInvoices_EmptyAfterProvision(t *testing.T) {
	pool := testPoolBilling(t)

	const orgID = "00000000-0000-0000-0000-000000000b02"
	t.Cleanup(func() { cleanupBillingByOrganization(pool, orgID) })

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEvent(orgID)); err != nil {
		t.Fatalf("provision subscription: %v", err)
	}

	e := billing.NewModuleEngine(pool, testAuthSubBilling, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/api/organizations/"+orgID+"/billing/invoices", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	// nil data slice is omitted from JSON; a present data array must be empty
	if data, ok := resp["data"]; ok && data != nil {
		if arr, ok := data.([]any); ok && len(arr) != 0 {
			t.Errorf("expected 0 invoices, got %d", len(arr))
		}
	}
}

func TestIntegration_GetSubscription_NotFoundWithoutProvision(t *testing.T) {
	pool := testPoolBilling(t)

	e := billing.NewModuleEngine(pool, testAuthSubBilling, "00000000-0000-0000-0000-000000000b99")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/api/organizations/00000000-0000-0000-0000-000000000b99/billing", ""))
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestIntegration_IdempotentProvision(t *testing.T) {
	pool := testPoolBilling(t)

	const orgID = "00000000-0000-0000-0000-000000000b03"
	t.Cleanup(func() { cleanupBillingByOrganization(pool, orgID) })

	mod := billing.NewModuleForTest(pool, nil)
	body := encodeOrganizationCreatedEvent(orgID)
	// two provisions for the same organization must not error (ON CONFLICT DO NOTHING)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), body); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), body); err != nil {
		t.Fatalf("second provision (idempotent): %v", err)
	}
}

// TestIntegration_ProvisionSubscription_PlanCatalogFailurePropagates is a
// regression test: provisionSubscription's plan catalog lookup reads via
// the pool, not the transaction-scoped querier, so a failure there isn't
// caught by Postgres aborting the transaction the way
// every other write in this function is — it must be propagated explicitly.
// Before the fix, this call's error was swallowed (`planInfo, _ = ...`),
// leaving subtotal at 0 and silently skipping invoice creation with no
// error at all — a silently free, unbilled subscription. Simulated via a
// redelivery carrying an unknown plan value: insertSubscription's
// ON CONFLICT DO NOTHING skips the FK check for the conflicting
// (already-provisioned) row, so this exercises the exact same "everything
// else in the transaction succeeds, only the plan lookup fails" path a
// transient lookup failure would.
func TestIntegration_ProvisionSubscription_PlanCatalogFailurePropagates(t *testing.T) {
	pool := testPoolBilling(t)

	const orgID = "00000000-0000-0000-0000-000000000b06"
	t.Cleanup(func() { cleanupBillingByOrganization(pool, orgID) })

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(),
		encodeOrganizationCreatedEventWithPlan(orgID, testAuthSubBilling, "solo", "monthly")); err != nil {
		t.Fatalf("first provision: %v", err)
	}

	badPlanBody := encodeOrganizationCreatedEventWithPlan(orgID, testAuthSubBilling, "does-not-exist", "monthly")
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), badPlanBody); err == nil {
		t.Fatal("redelivery with an unknown plan: want an error from the plan catalog lookup, got nil")
	}
}

// --- contracts.CatalogReader: catalog methods (moved from reference module,
// which used to query billing.* tables directly across a schema boundary it
// didn't own — see repository.go's "catalog" section) ---

// newCatalogModule returns a Module whose exported CatalogReader methods
// read real seed data — Module always reads its own schema directly.
func newCatalogModule(pool *pgxpool.Pool) *billing.Module {
	return billing.NewModuleForTest(pool, nil)
}

func TestIntegration_ListPlans_ReturnsSeedData(t *testing.T) {
	mod := newCatalogModule(testPoolBilling(t))

	plans, err := mod.ListPlans(t.Context())
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}
	if len(plans) < 3 {
		t.Errorf("want >= 3 plans, got %d", len(plans))
	}

	seen := make(map[string]bool, len(plans))
	for _, p := range plans {
		seen[p.ID] = true
		if p.Prices == nil {
			t.Errorf("plan %q: want non-nil prices", p.ID)
		}
	}
	for _, id := range []string{"solo", "growth", "custom"} {
		if !seen[id] {
			t.Errorf("expected seeded plan %q in results", id)
		}
	}
}

func TestIntegration_GetPlanByID_Known(t *testing.T) {
	mod := newCatalogModule(testPoolBilling(t))

	plan, err := mod.GetPlanByID(t.Context(), "solo")
	if err != nil {
		t.Fatalf("GetPlanByID solo: %v", err)
	}
	if plan.ID != "solo" {
		t.Errorf("want id=solo, got %q", plan.ID)
	}
	if _, ok := plan.Prices["USD"]; !ok {
		t.Error("want USD price entry in solo plan")
	}
	if plan.Limits["members"] != 1 {
		t.Errorf("want members limit=1, got %d", plan.Limits["members"])
	}
	if plan.SortOrder != 1 {
		t.Errorf("want sort_order=1 for solo, got %d", plan.SortOrder)
	}
	// solo has zero boolean/static plan_features rows (only metered +
	// config), so this is exactly the case that previously left Features
	// as a nil slice — serializes to JSON `null` and crashes any frontend
	// code calling .slice()/.map() on plan.features without a null check.
	if plan.Features == nil {
		t.Error("want non-nil (possibly empty) Features slice for solo plan, got nil")
	}

	var rateLimit struct {
		RequestsPerMinute int `json:"requests_per_minute"`
	}
	raw, ok := plan.ConfigValues["api_rate_limit"]
	if !ok {
		t.Fatal("want api_rate_limit config_value on solo plan")
	}
	if err := json.Unmarshal(raw, &rateLimit); err != nil {
		t.Fatalf("unmarshal api_rate_limit config_value: %v", err)
	}
	if rateLimit.RequestsPerMinute != 120 {
		t.Errorf("want requests_per_minute=120 for solo, got %d", rateLimit.RequestsPerMinute)
	}
}

func TestIntegration_GetPlanByID_NotFound(t *testing.T) {
	mod := newCatalogModule(testPoolBilling(t))

	_, err := mod.GetPlanByID(t.Context(), "nonexistent")
	if err == nil {
		t.Error("want error for unknown plan, got nil")
	}
}

func TestIntegration_ListFeatures_Catalog(t *testing.T) {
	mod := newCatalogModule(testPoolBilling(t))

	features, err := mod.ListFeatures(t.Context())
	if err != nil {
		t.Fatalf("ListFeatures: %v", err)
	}
	if len(features) < 12 {
		t.Errorf("want >= 12 seeded features, got %d", len(features))
	}

	byID := make(map[string]string, len(features)) // id -> type
	for _, f := range features {
		byID[f.ID] = f.Type
	}
	if byID["members"] != "metered" {
		t.Errorf("want members type=metered, got %q", byID["members"])
	}
	if byID["api_rate_limit"] != "config" {
		t.Errorf("want api_rate_limit type=config, got %q", byID["api_rate_limit"])
	}
}

func TestIntegration_ListAddons_Catalog(t *testing.T) {
	mod := newCatalogModule(testPoolBilling(t))

	addons, err := mod.ListAddons(t.Context())
	if err != nil {
		t.Fatalf("ListAddons: %v", err)
	}
	if len(addons) < 1 {
		t.Errorf("want >= 1 seeded addon, got %d", len(addons))
	}

	var extraSeat *int
	for _, a := range addons {
		if a.ID != "extra-seat" {
			continue
		}
		if _, ok := a.Prices["USD"]; !ok {
			t.Error("want USD price entry in extra-seat addon")
		}
		v := a.Features["members"]
		extraSeat = &v
	}
	if extraSeat == nil {
		t.Fatal("expected seeded addon extra-seat in results")
	}
	if *extraSeat != 1 {
		t.Errorf("want extra-seat members delta=1, got %d", *extraSeat)
	}
}
