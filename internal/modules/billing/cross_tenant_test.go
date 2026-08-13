package billing_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/messaging"
	"github.com/aasumitro/stratum/internal/testsupport"
)

// Duplicated rather than shared with organization's own unexported
// testSecretEncryptionKey — this external test package (billing_test) cannot
// see it, and the actual value is irrelevant here since no test in this file
// exercises webhook encrypt/decrypt.
const testWebhookEncryptionKey = "test-webhook-secret-encryption-key-0000"

// newBillingCrossTenantEngine wires the real middleware.NewOrganizationMiddleware
// (backed by a real organization.Module reading the same DB rows
// testsupport.SeedTwoOrgs seeds) together with a real billing.Module's
// Register — the same three real components
// internal/app/bootstrap/rate_limit_ordering_test.go wires for the identical
// reason: billing's own export_test.go helpers (NewModuleEngine and
// friends) fake the Org dependency as a fixed context set from a
// constructor argument, never consulting the request's :organizationID path
// param at all. That fake is fine for every other billing test (which only
// ever calls its own engine's own fixed org), but it cannot produce a real
// 403 for a foreign :organizationID — exactly the thing a membership-only
// cross-tenant case needs to prove. RLS is also real (middleware.NewRLSTxMiddleware),
// not a no-op like every other billing test engine in this package:
// db.RequireTx panics if a FOR UPDATE lock query (e.g. detachAddon's
// lockSubscriptionForUpdate) runs without an ambient transaction, and in
// production the billing route group's RLS middleware is what supplies that
// transaction — a no-op here would panic on exactly the routes this harness
// needs to probe. Only applies to the `billing` group; billingPay
// deliberately never receives deps.RLS in module.go's own Register (see its
// comment there), so the billingPay cases below are unaffected.
// RateLimit/Idempotency/MFA stay no-ops.
func newBillingCrossTenantEngine(pool *pgxpool.Pool, authSub string) (*gin.Engine, *billing.Module) {
	gin.SetMode(gin.TestMode)
	orgMod := organization.New(pool, messaging.NoopPublisher{}, testWebhookEncryptionKey)
	billingMod := billing.New(pool, messaging.NoopPublisher{}, billing.ProviderConfig{}, nil, nil, nil)
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub})
		c.Next()
	}
	noop := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	billingMod.Register(api, httpserver.RouteDeps{
		Auth: authMW, Org: middleware.NewOrganizationMiddleware(orgMod),
		RateLimit: noop, RLS: middleware.NewRLSTxMiddleware(pool), Idempotency: noop, MFA: noop,
	})
	return e, billingMod
}

// provisionTwoOrgSubscriptions gives both orgs in two an active subscription
// via the real HandleOrganizationCreated worker path (not the HTTP API —
// faster, matches billing/repository_test.go's
// mod.Worker.HandleOrganizationCreated convention used throughout this
// package), so billing routes under test don't 402/403 on "no subscription"
// before ever reaching the cross-tenant check under test.
//
// Deliberately does NOT call repository_test.go's setupBillingTest here —
// its upfront cleanupBillingByOrganization call ends with
// "DELETE FROM organization.organizations WHERE id = $1", a defensive
// no-op for that file's own fixtures (which use a fixed literal org UUID
// that's never actually inserted into organization.organizations). Composed
// with testsupport.SeedTwoOrgs's real, freshly-seeded org rows, that same
// line deletes them out from under this test before HandleOrganizationCreated
// even runs — every case then hit the Org middleware's "organization not
// found" 404 instead of the real membership/ownership check under test, a
// false pass for every case that happened to also expect 404. Discovered by
// tracing why a first draft of this file's membership-only cases returned
// 404 instead of the expected 403; fixed by registering only the
// billing-schema cleanup (reusing cleanupBillingByOrganization's own
// statements, minus that last line) — no upfront pre-clean needed either,
// since SeedTwoOrgs always mints a fresh gen_random_uuid() with nothing
// left over from a previous run to guard against.
func provisionTwoOrgSubscriptions(t *testing.T, pool *pgxpool.Pool, mod *billing.Module, two testsupport.TwoOrgs) {
	t.Helper()
	cleanupBillingRowsOnly(t, pool, two.OrgA)
	cleanupBillingRowsOnly(t, pool, two.OrgB)
	for _, pair := range [][2]string{{two.OrgA, two.OwnerA}, {two.OrgB, two.OwnerB}} {
		if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(pair[0], pair[1])); err != nil {
			t.Fatalf("provision %s: %v", pair[0], err)
		}
	}
}

// cleanupBillingRowsOnly registers a t.Cleanup that deletes organizationID's
// billing-schema rows — the same statements as repository_test.go's
// cleanupBillingByOrganization, minus its final DELETE against
// organization.organizations (see provisionTwoOrgSubscriptions' comment for
// why that line can't be reused here).
func cleanupBillingRowsOnly(t *testing.T, pool *pgxpool.Pool, organizationID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM messaging.outbox WHERE payload->>'org_id' = $1`, organizationID)
		pool.Exec(ctx, `
			DELETE FROM billing.webhook_events we
			WHERE EXISTS (
				SELECT 1 FROM billing.payment_links pl
				JOIN billing.invoices i ON i.id = pl.invoice_id
				JOIN billing.subscriptions s ON s.id = i.subscription_id
				WHERE s.subject_type = 'organization' AND s.subject_id = $1
				  AND we.event_id LIKE pl.external_id || '%'
			)`, organizationID)
		pool.Exec(ctx, `DELETE FROM billing.payments WHERE invoice_id IN (SELECT i.id FROM billing.invoices i JOIN billing.subscriptions s ON s.id = i.subscription_id WHERE s.subject_type = 'organization' AND s.subject_id = $1)`, organizationID)
		pool.Exec(ctx, `DELETE FROM billing.payment_links WHERE invoice_id IN (SELECT i.id FROM billing.invoices i JOIN billing.subscriptions s ON s.id = i.subscription_id WHERE s.subject_type = 'organization' AND s.subject_id = $1)`, organizationID)
		pool.Exec(ctx, `DELETE FROM billing.subscription_history WHERE subscription_id IN (SELECT id FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1)`, organizationID)
		pool.Exec(ctx, `DELETE FROM billing.subscription_addons WHERE subscription_id IN (SELECT id FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1)`, organizationID)
		pool.Exec(ctx, `DELETE FROM billing.coupon_redemptions WHERE subscription_id IN (SELECT id FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1)`, organizationID)
		pool.Exec(ctx, `DELETE FROM billing.invoice_line_items WHERE invoice_id IN (SELECT i.id FROM billing.invoices i JOIN billing.subscriptions s ON s.id = i.subscription_id WHERE s.subject_type = 'organization' AND s.subject_id = $1)`, organizationID)
		pool.Exec(ctx, `DELETE FROM billing.invoices WHERE subscription_id IN (SELECT id FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1)`, organizationID)
		pool.Exec(ctx, `DELETE FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1`, organizationID)
	})
}

// TestCrossTenant_Subscription covers billing's subscription-management
// routes, all membership-only (no sub-resource ID
// beyond :organizationID) — org A's owner acting on org B's ID must get the
// real Org middleware's 403.
func TestCrossTenant_Subscription(t *testing.T) {
	pool := testPoolBilling(t)
	two := testsupport.SeedTwoOrgs(t, pool)
	engine, mod := newBillingCrossTenantEngine(pool, two.OwnerA)
	provisionTwoOrgSubscriptions(t, pool, mod, two)

	cases := []testsupport.CrossTenantCase{
		{Name: "getSubscription", Method: http.MethodGet, Path: billingURL(two.OrgB), WantStatus: http.StatusForbidden},
		{Name: "changePlan", Method: http.MethodPatch, Path: billingURL(two.OrgB) + "/plan", WantStatus: http.StatusForbidden},
		{Name: "downgradeSubscription", Method: http.MethodPost, Path: billingURL(two.OrgB) + "/downgrade", WantStatus: http.StatusForbidden},
		{Name: "undoDowngrade", Method: http.MethodPost, Path: billingURL(two.OrgB) + "/downgrade/undo", WantStatus: http.StatusForbidden},
		{Name: "cancelSubscription", Method: http.MethodPost, Path: billingURL(two.OrgB) + "/cancel", WantStatus: http.StatusForbidden},
		{Name: "undoCancellation", Method: http.MethodPost, Path: billingURL(two.OrgB) + "/cancel/undo", WantStatus: http.StatusForbidden},
	}
	testsupport.RunCrossTenantCases(t, engine, pool, two.OwnerA, cases)
}

// TestCrossTenant_InvoiceHistory covers billing's invoice/payment/history
// routes. invoicePDF is the one sub-resource-bearing route here
// (:invoiceID) — getInvoicePDFData maps any lookup failure to 404
// (service_invoice.go's own comment: "maps any failure to 404 — matches the
// pre-migration handler"), confirmed by reading the code, not assumed.
func TestCrossTenant_InvoiceHistory(t *testing.T) {
	pool := testPoolBilling(t)
	two := testsupport.SeedTwoOrgs(t, pool)
	engine, mod := newBillingCrossTenantEngine(pool, two.OwnerA)
	provisionTwoOrgSubscriptions(t, pool, mod, two)

	subB := getSubscriptionID(pool, two.OrgB)
	invoiceB := seedInvoice(pool, subB, "USD", 900)

	cases := []testsupport.CrossTenantCase{
		{Name: "listInvoices", Method: http.MethodGet, Path: billingURL(two.OrgB) + "/invoices", WantStatus: http.StatusForbidden},
		{Name: "listPaymentLinks", Method: http.MethodGet, Path: billingURL(two.OrgB) + "/payment-links", WantStatus: http.StatusForbidden},
		{Name: "previewInvoice", Method: http.MethodGet, Path: billingURL(two.OrgB) + "/preview", WantStatus: http.StatusForbidden},
		{Name: "listHistory", Method: http.MethodGet, Path: billingURL(two.OrgB) + "/history", WantStatus: http.StatusForbidden},
		{Name: "listPayments", Method: http.MethodGet, Path: billingURL(two.OrgB) + "/payments", WantStatus: http.StatusForbidden},
		{Name: "invoicePDF_membershipOnly", Method: http.MethodGet, Path: billingURL(two.OrgB) + "/invoices/" + invoiceB + "/pdf", WantStatus: http.StatusForbidden},
		{Name: "invoicePDF_crossOrgInvoiceID", Method: http.MethodGet, Path: billingURL(two.OrgA) + "/invoices/" + invoiceB + "/pdf", WantStatus: http.StatusNotFound},
	}
	testsupport.RunCrossTenantCases(t, engine, pool, two.OwnerA, cases)
}

// TestCrossTenant_AddonUsageCoupon covers billing's addon/usage/coupon
// routes. detachAddon and undoAddonQuantityChange are the two
// sub-resource-bearing routes (:addonID) — both resolve the caller's *own*
// subscription first (via organizationID, already membership-checked) and
// then look up addonID scoped to that subscription (findAttachedAddon), so
// an addon only attached to org B is a plain 404 on org A's own
// subscription regardless of what org B has — confirmed by reading
// service_addon.go's detachAddon/undoScheduledAddonQuantityChange.
func TestCrossTenant_AddonUsageCoupon(t *testing.T) {
	pool := testPoolBilling(t)
	two := testsupport.SeedTwoOrgs(t, pool)
	engine, mod := newBillingCrossTenantEngine(pool, two.OwnerA)
	provisionTwoOrgSubscriptions(t, pool, mod, two)

	subB := getSubscriptionID(pool, two.OrgB)
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO billing.subscription_addons (subscription_id, addon_id, quantity) VALUES ($1, 'extra-seat', 1)`, subB); err != nil {
		t.Fatalf("attach addon to org B: %v", err)
	}

	cases := []testsupport.CrossTenantCase{
		{Name: "listAddons", Method: http.MethodGet, Path: billingURL(two.OrgB) + "/addons", WantStatus: http.StatusForbidden},
		{Name: "getUsage", Method: http.MethodGet, Path: billingURL(two.OrgB) + "/usage", WantStatus: http.StatusForbidden},
		{Name: "recordUsage", Method: http.MethodPost, Path: billingURL(two.OrgB) + "/usage", WantStatus: http.StatusForbidden},
		{Name: "listFeatures", Method: http.MethodGet, Path: billingURL(two.OrgB) + "/features", WantStatus: http.StatusForbidden},
		{Name: "redeemCoupon", Method: http.MethodPost, Path: billingURL(two.OrgB) + "/coupons/redeem", WantStatus: http.StatusForbidden},
		{Name: "listEligibleCoupons", Method: http.MethodGet, Path: billingURL(two.OrgB) + "/coupons", WantStatus: http.StatusForbidden},
		{Name: "detachAddon_membershipOnly", Method: http.MethodDelete, Path: billingURL(two.OrgB) + "/addons/extra-seat", WantStatus: http.StatusForbidden},
		{Name: "detachAddon_crossOrgAddonID", Method: http.MethodDelete, Path: billingURL(two.OrgA) + "/addons/extra-seat", WantStatus: http.StatusNotFound},
		{Name: "undoAddonQuantityChange_membershipOnly", Method: http.MethodPost, Path: billingURL(two.OrgB) + "/addons/extra-seat/undo", WantStatus: http.StatusForbidden},
		{Name: "undoAddonQuantityChange_crossOrgAddonID", Method: http.MethodPost, Path: billingURL(two.OrgA) + "/addons/extra-seat/undo", WantStatus: http.StatusNotFound},
	}
	testsupport.RunCrossTenantCases(t, engine, pool, two.OwnerA, cases)
}

// TestCrossTenant_BillingPay covers the billingPay route group
// (resume/extend/activate/pay/addons — deliberately outside the RLS-wrapped
// transaction, since these routes make blocking Stripe/Xendit calls; worth
// confirming they're still correctly ownership-checked despite that).
// createPaymentLink is the one
// sub-resource-bearing route here (:invoiceID) — it funnels through the
// same createPaymentLink/createPaymentLinkForOwner path
// TestIntegration_RegeneratePaymentLink_CrossTenant_404_NoMutation already
// proved returns 404 for a foreign invoice ID (createPaymentLinkForOwner's
// own defer maps pgx.ErrNoRows to apperr.NotFound), confirmed again here by
// reading service_invoice.go directly rather than assuming the same status
// carries over. attachAddon's addonID (in the request body) is a global
// catalog ID, not another org's resource, so it stays membership-only per
// the route inventory's classification.
func TestCrossTenant_BillingPay(t *testing.T) {
	pool := testPoolBilling(t)
	two := testsupport.SeedTwoOrgs(t, pool)
	engine, mod := newBillingCrossTenantEngine(pool, two.OwnerA)
	provisionTwoOrgSubscriptions(t, pool, mod, two)

	subB := getSubscriptionID(pool, two.OrgB)
	invoiceB := seedInvoice(pool, subB, "USD", 900)

	cases := []testsupport.CrossTenantCase{
		{Name: "resumeSubscription", Method: http.MethodPost, Path: billingURL(two.OrgB) + "/resume", WantStatus: http.StatusForbidden},
		{Name: "extendSubscription", Method: http.MethodPost, Path: billingURL(two.OrgB) + "/extend", WantStatus: http.StatusForbidden},
		{Name: "activateTrialNow", Method: http.MethodPost, Path: billingURL(two.OrgB) + "/activate", WantStatus: http.StatusForbidden},
		{Name: "attachAddon", Method: http.MethodPost, Path: billingURL(two.OrgB) + "/addons", Body: `{"addon_id":"extra-seat"}`, WantStatus: http.StatusForbidden},
		{Name: "createPaymentLink_membershipOnly", Method: http.MethodPost, Path: billingURL(two.OrgB) + "/invoices/" + invoiceB + "/pay", WantStatus: http.StatusForbidden},
		{Name: "createPaymentLink_crossOrgInvoiceID", Method: http.MethodPost, Path: billingURL(two.OrgA) + "/invoices/" + invoiceB + "/pay", WantStatus: http.StatusNotFound},
	}
	testsupport.RunCrossTenantCases(t, engine, pool, two.OwnerA, cases)
}
