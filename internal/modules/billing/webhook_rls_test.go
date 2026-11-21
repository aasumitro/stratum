package billing_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/platform/db"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// webhookRLSPools opens the three roles a real deployment splits webhook
// processing across: the migration/superuser role (fixture cleanup, and
// reading back results with no RLS in the way), the RLS-constrained role an
// authenticated request actually runs under, and the BYPASSRLS role
// processWebhook now uses. Skips the test if any of the three isn't
// configured, matching rls_isolation_test.go's own guard.
func webhookRLSPools(t *testing.T) (testPool, appPool, webhookPool *pgxpool.Pool) {
	t.Helper()
	testDBURL := os.Getenv("TEST_DATABASE_URL")
	appURL := os.Getenv("POSTGRES_APP_URL")
	webhookURL := os.Getenv("POSTGRES_WEBHOOK_URL")
	if testDBURL == "" || appURL == "" || webhookURL == "" {
		t.Skip("TEST_DATABASE_URL, POSTGRES_APP_URL, and POSTGRES_WEBHOOK_URL required for webhook RLS tests")
	}

	ctx := t.Context()
	var err error
	testPool, err = pgxpool.New(ctx, testDBURL)
	if err != nil {
		t.Fatalf("failed to connect test pool: %v", err)
	}
	t.Cleanup(testPool.Close)

	appPool, err = pgxpool.New(ctx, appURL)
	if err != nil {
		t.Fatalf("failed to connect app pool: %v", err)
	}
	t.Cleanup(appPool.Close)

	webhookPool, err = pgxpool.New(ctx, webhookURL)
	if err != nil {
		t.Fatalf("failed to connect webhook pool: %v", err)
	}
	t.Cleanup(webhookPool.Close)
	return testPool, appPool, webhookPool
}

// seedWebhookFixture creates an organization, an active subscription, a
// pending renewal invoice, and a pending payment link for it — using appPool
// (the RLS-constrained stratum_app role) with the org context an
// authenticated request would actually carry, so the fixture only exists
// because a normal write reached it, not because it was inserted by a
// role that bypasses RLS. The organization row itself goes through
// testPool since organization.organizations carries no RLS policy at all
// (see billing.md's note on which routes/tables use it).
func seedWebhookFixture(t *testing.T, testPool, appPool *pgxpool.Pool, orgID, ownerSub, provider, currency string, amountCents int64) (invoiceID string) {
	t.Helper()
	ctx := t.Context()

	if _, err := testPool.Exec(ctx, `
		INSERT INTO organization.organizations (id, name, slug, owner_id) VALUES ($1, $2, $3, $4)`,
		orgID, "Webhook RLS Org "+provider, "webhook-rls-"+provider, ownerSub,
	); err != nil {
		t.Fatalf("seed organization: %v", err)
	}

	appTx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin app tx: %v", err)
	}
	if err := db.SetOrgContext(ctx, appTx, orgID); err != nil {
		t.Fatalf("set org context: %v", err)
	}

	periodStart := time.Now().Add(-25 * 24 * time.Hour)
	periodEnd := time.Now().Add(5 * 24 * time.Hour)
	var subID string
	if err := appTx.QueryRow(ctx, `
		INSERT INTO billing.subscriptions (subject_type, subject_id, plan, status, currency, period_start, period_end)
		VALUES ('organization', $1, 'solo', 'active', $2, $3, $4) RETURNING id`,
		orgID, currency, periodStart, periodEnd,
	).Scan(&subID); err != nil {
		t.Fatalf("seed subscription (as stratum_app): %v", err)
	}

	if err := appTx.QueryRow(ctx, `
		INSERT INTO billing.invoices (subscription_id, amount_cents, currency, status, kind)
		VALUES ($1, $2, $3, 'pending', 'subscription') RETURNING id`,
		subID, amountCents, currency,
	).Scan(&invoiceID); err != nil {
		t.Fatalf("seed invoice (as stratum_app): %v", err)
	}

	extID := provider + "_link_" + orgID
	if _, err := appTx.Exec(ctx, `
		INSERT INTO billing.payment_links (invoice_id, provider, currency, amount_cents, external_id, url)
		VALUES ($1, $2, $3, $4, $5, 'https://pay.example.com')`,
		invoiceID, provider, currency, amountCents, extID,
	); err != nil {
		t.Fatalf("seed payment link (as stratum_app): %v", err)
	}

	if err := appTx.Commit(ctx); err != nil {
		t.Fatalf("commit fixture tx: %v", err)
	}
	return invoiceID
}

func cleanupWebhookFixture(testPool *pgxpool.Pool, orgID string) {
	ctx := context.Background()
	// A successful run's applyRenewalPayment writes a subscription_history
	// row (the renewal record) — without deleting it first, the FK from
	// subscription_history to subscriptions silently blocks the
	// subscriptions delete below (Exec errors are discarded, matching this
	// package's other cleanup helpers), leaking the fixture into the next
	// run and colliding with its (subject_type, subject_id) unique index.
	testPool.Exec(ctx, `DELETE FROM messaging.outbox WHERE payload->>'org_id' = $1`, orgID)
	testPool.Exec(ctx, `DELETE FROM billing.webhook_events we WHERE EXISTS (
		SELECT 1 FROM billing.payment_links pl
		JOIN billing.invoices i ON i.id = pl.invoice_id
		JOIN billing.subscriptions s ON s.id = i.subscription_id
		WHERE s.subject_id = $1 AND we.event_id LIKE pl.external_id || '%')`, orgID)
	testPool.Exec(ctx, `DELETE FROM billing.payments WHERE invoice_id IN (
		SELECT i.id FROM billing.invoices i JOIN billing.subscriptions s ON s.id = i.subscription_id WHERE s.subject_id = $1)`, orgID)
	testPool.Exec(ctx, `DELETE FROM billing.payment_links WHERE invoice_id IN (
		SELECT i.id FROM billing.invoices i JOIN billing.subscriptions s ON s.id = i.subscription_id WHERE s.subject_id = $1)`, orgID)
	testPool.Exec(ctx, `DELETE FROM billing.subscription_history WHERE subscription_id IN (
		SELECT id FROM billing.subscriptions WHERE subject_id = $1)`, orgID)
	testPool.Exec(ctx, `DELETE FROM billing.subscription_addons WHERE subscription_id IN (
		SELECT id FROM billing.subscriptions WHERE subject_id = $1)`, orgID)
	testPool.Exec(ctx, `DELETE FROM billing.invoice_line_items WHERE invoice_id IN (
		SELECT i.id FROM billing.invoices i JOIN billing.subscriptions s ON s.id = i.subscription_id WHERE s.subject_id = $1)`, orgID)
	testPool.Exec(ctx, `DELETE FROM billing.invoices WHERE subscription_id IN (
		SELECT id FROM billing.subscriptions WHERE subject_id = $1)`, orgID)
	testPool.Exec(ctx, `DELETE FROM billing.subscriptions WHERE subject_id = $1`, orgID)
	testPool.Exec(ctx, `DELETE FROM organization.organizations WHERE id = $1`, orgID)
}

// webhookEngine wires the real webhook routes against a service whose pool
// is the RLS-constrained appPool and whose webhookPool is the pool passed
// in — this lets the caller swap webhookPool between appPool (reproducing
// the pre-fix bug) and a real BYPASSRLS pool (the fix) without touching
// production code.
func webhookEngine(t *testing.T, appPool, webhookPool *pgxpool.Pool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mod := billing.New(appPool, billing.ProviderConfig{}, nil, nil, webhookPool)
	e := gin.New()
	if err := mod.RegisterWebhooks(e.Group("/webhooks")); err != nil {
		t.Fatalf("register webhooks: %v", err)
	}
	return e
}

// TestWebhookRLS_XenditPayment_CommitsWithoutOrgContext exercises the real
// Xendit webhook route against the real RLS-constrained role: a
// subscription, invoice, and payment link are seeded exactly as an
// authenticated request would (stratum_app, org context set), then the
// webhook route is called with no org context at all — the situation
// every real webhook delivery is actually in, since the provider carries
// no user session. If processWebhook ever runs under the same
// RLS-constrained pool as everything else instead of its own BYPASSRLS
// pool, every query inside it returns zero rows and the whole confirmation
// silently rolls back while the route still answers 200 OK — this asserts
// the payment actually applies instead.
func TestWebhookRLS_XenditPayment_CommitsWithoutOrgContext(t *testing.T) {
	testPool, appPool, webhookPool := webhookRLSPools(t)

	const orgID = "00000000-0000-0000-0000-000000000fa1"
	const ownerSub = "webhook_rls_xendit_owner"
	t.Cleanup(func() { cleanupWebhookFixture(testPool, orgID) })
	cleanupWebhookFixture(testPool, orgID) // pre-cleanup in case a previous run panicked before t.Cleanup

	invoiceID := seedWebhookFixture(t, testPool, appPool, orgID, ownerSub, "xendit", "IDR", 135000)
	extID := "xendit_link_" + orgID

	e := webhookEngine(t, appPool, webhookPool)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/xendit",
		`{"id":"`+extID+`","status":"PAID"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", w.Code, w.Body)
	}

	ctx := t.Context()
	var invStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM billing.invoices WHERE id = $1`, invoiceID).Scan(&invStatus); err != nil {
		t.Fatalf("read back invoice: %v", err)
	}
	if invStatus != "paid" {
		t.Errorf("invoice: want status=paid, got %q — webhook-driven payment confirmation silently failed to apply", invStatus)
	}

	var webhookEventCount int
	testPool.QueryRow(ctx, `SELECT COUNT(*) FROM billing.webhook_events WHERE provider = 'xendit' AND event_id = $1`,
		extID+"_PAID").Scan(&webhookEventCount)
	if webhookEventCount != 1 {
		t.Errorf("want 1 webhook_events dedup row, got %d", webhookEventCount)
	}

	var paymentCount int
	testPool.QueryRow(ctx, `SELECT COUNT(*) FROM billing.payments WHERE invoice_id = $1`, invoiceID).Scan(&paymentCount)
	if paymentCount != 1 {
		t.Errorf("want 1 billing.payments row, got %d", paymentCount)
	}
}

// TestWebhookRLS_StripePayment_CommitsWithoutOrgContext is the smaller
// Stripe-side counterpart — handleStripeWebhook reaches the same
// processWebhook call chain as handleXenditWebhook, so only the invoice
// status is re-checked here rather than repeating every assertion above.
func TestWebhookRLS_StripePayment_CommitsWithoutOrgContext(t *testing.T) {
	testPool, appPool, webhookPool := webhookRLSPools(t)

	const orgID = "00000000-0000-0000-0000-000000000fa2"
	const ownerSub = "webhook_rls_stripe_owner"
	t.Cleanup(func() { cleanupWebhookFixture(testPool, orgID) })
	cleanupWebhookFixture(testPool, orgID)

	invoiceID := seedWebhookFixture(t, testPool, appPool, orgID, ownerSub, "stripe", "USD", 900)
	extID := "stripe_link_" + orgID

	e := webhookEngine(t, appPool, webhookPool)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe",
		`{"type":"checkout.session.completed","data":{"object":{"id":"`+extID+`","payment_status":"paid"}}}`))
	if w.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", w.Code, w.Body)
	}

	var invStatus string
	if err := testPool.QueryRow(t.Context(), `SELECT status FROM billing.invoices WHERE id = $1`, invoiceID).Scan(&invStatus); err != nil {
		t.Fatalf("read back invoice: %v", err)
	}
	if invStatus != "paid" {
		t.Errorf("invoice: want status=paid, got %q — webhook-driven payment confirmation silently failed to apply", invStatus)
	}
}

// The gate's caller holds no transaction. Without an organization context, FORCE RLS
// hides the invoice, the read finds nothing, and every unpaid organization would be
// reported as paid up, which no error would ever reveal.
func TestWebhookRLS_HasUnpaidActivation_SeesTheInvoiceWithNoAmbientTransaction(t *testing.T) {
	testPool, appPool, _ := webhookRLSPools(t)
	const (
		orgID    = "00000000-0000-0000-0000-000000000fa4"
		ownerSub = "webhook_rls_gate_owner"
	)
	t.Cleanup(func() { cleanupWebhookFixture(testPool, orgID) })
	cleanupWebhookFixture(testPool, orgID)

	invoiceID := seedWebhookFixture(t, testPool, appPool, orgID, ownerSub, "stripe", "USD", 900)
	mod := billing.New(appPool, billing.ProviderConfig{}, nil, nil, nil)

	unpaid, err := mod.HasUnpaidActivation(t.Context(), orgID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if unpaid {
		t.Error("a pending renewal must not read as an unpaid activation")
	}

	if _, err := testPool.Exec(t.Context(), `UPDATE billing.invoices SET kind = 'activation' WHERE id = $1`, invoiceID); err != nil {
		t.Fatalf("set kind: %v", err)
	}
	unpaid, err = mod.HasUnpaidActivation(t.Context(), orgID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !unpaid {
		t.Error("want the pending activation seen under stratum_app with no ambient transaction")
	}

	if _, err := testPool.Exec(t.Context(), `UPDATE billing.invoices SET status = 'paid', paid_at = now() WHERE id = $1`, invoiceID); err != nil {
		t.Fatalf("pay: %v", err)
	}
	unpaid, err = mod.HasUnpaidActivation(t.Context(), orgID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if unpaid {
		t.Error("paying must lift it at once")
	}
}

// The reissue runs in the webhook's transaction, under the webhook's own role, and
// touches tables a payment confirmation otherwise does not write.
func TestWebhookRLS_AddonPaid_ReissuesTheRenewalUnderTheWebhookRole(t *testing.T) {
	testPool, appPool, webhookPool := webhookRLSPools(t)
	const (
		orgID    = "00000000-0000-0000-0000-000000000fa5"
		ownerSub = "webhook_rls_reissue_owner"
	)
	t.Cleanup(func() { cleanupWebhookFixture(testPool, orgID) })
	cleanupWebhookFixture(testPool, orgID)

	renewal := seedWebhookFixture(t, testPool, appPool, orgID, ownerSub, "stripe", "USD", 900)
	ctx := t.Context()
	var subID, addonInvoice string
	if err := testPool.QueryRow(ctx, `SELECT subscription_id FROM billing.invoices WHERE id = $1`, renewal).Scan(&subID); err != nil {
		t.Fatalf("read subscription: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO billing.invoices (subscription_id, amount_cents, currency, kind)
		VALUES ($1, 300, 'USD', 'addon_increase') RETURNING id`, subID).Scan(&addonInvoice); err != nil {
		t.Fatalf("seed add-on invoice: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO billing.subscription_addons (subscription_id, addon_id, quantity, pending_quantity, pending_invoice_id)
		VALUES ($1, 'extra-seat', 0, 3, $2)`, subID, addonInvoice); err != nil {
		t.Fatalf("seed pending increase: %v", err)
	}
	extID := "stripe_addon_" + orgID
	if _, err := testPool.Exec(ctx, `
		INSERT INTO billing.payment_links (invoice_id, provider, currency, amount_cents, external_id, url)
		VALUES ($1, 'stripe', 'USD', 300, $2, 'https://pay.example.com')`, addonInvoice, extID); err != nil {
		t.Fatalf("seed link: %v", err)
	}

	w := httptest.NewRecorder()
	webhookEngine(t, appPool, webhookPool).ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/stripe",
		`{"type":"checkout.session.completed","data":{"object":{"id":"`+extID+`","payment_status":"paid"}}}`))
	if w.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", w.Code, w.Body)
	}

	var status string
	if err := testPool.QueryRow(ctx, `SELECT status FROM billing.invoices WHERE id = $1`, renewal).Scan(&status); err != nil {
		t.Fatalf("read old renewal: %v", err)
	}
	if status != "void" {
		t.Errorf("old renewal: want void, got %q", status)
	}
	var amount int64
	var number *string
	if err := testPool.QueryRow(ctx, `
		SELECT amount_cents, invoice_number FROM billing.invoices
		WHERE subscription_id = $1 AND kind = 'subscription' AND status = 'pending'`, subID).Scan(&amount, &number); err != nil {
		t.Fatalf("read reissued renewal: %v", err)
	}
	if amount != 1200 {
		t.Errorf("reissued renewal: want 1200 (900 plan + 3 x 100 seats), got %d", amount)
	}
	if number == nil {
		t.Error("reissued renewal: want an invoice number")
	}
}
