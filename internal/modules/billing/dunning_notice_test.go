package billing_test

import (
	"encoding/json"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/billing"
)

// encodeInvoiceFailedBody builds the body the dunning handlers decode — only
// Data matters to events.Decode, so the envelope's other fields stay zero.
func encodeInvoiceFailedBody(t *testing.T, orgID, invoiceID string) []byte {
	t.Helper()
	body, err := json.Marshal(events.Envelope{
		Data: events.InvoiceFailed{OrgID: orgID, InvoiceID: invoiceID, FailedAt: time.Now()},
	})
	if err != nil {
		t.Fatalf("encode InvoiceFailed: %v", err)
	}
	return body
}

// seedDunningCase provisions orgID's subscription with subStatus and one
// invoice of the given kind and status, returning the invoice ID.
func seedDunningCase(t *testing.T, pool *pgxpool.Pool, orgID, subStatus, kind, invStatus string) string {
	t.Helper()
	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(),
		encodeOrganizationCreatedEventFor(orgID, "integ_billing_dunning_user_"+orgID[len(orgID)-3:])); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET status = $1, trial_end = NULL WHERE id = $2`, subStatus, subID); err != nil {
		t.Fatalf("set subscription status: %v", err)
	}
	var invID string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO billing.invoices (subscription_id, amount_cents, currency, kind, status)
		VALUES ($1, 900, 'USD', $2, $3) RETURNING id`,
		subID, kind, invStatus).Scan(&invID); err != nil {
		t.Fatalf("seed %s/%s invoice: %v", kind, invStatus, err)
	}
	return invID
}

// TestIntegration_DunningNotice_ForwardsOnlyWhileInvoiceIsDue covers every
// branch of the day-3 dunning gate: the scheduled notice is forwarded only
// while its invoice is still pending, backs the subscription's own period,
// and the subscription has not ended. A renewal that was paid, superseded
// (void) or failed by expiry, an additive invoice, and a cancelled
// subscription all drop it.
func TestIntegration_DunningNotice_ForwardsOnlyWhileInvoiceIsDue(t *testing.T) {
	cases := []struct {
		name, orgID, kind, invStatus, subStatus string
		want                                    int
	}{
		{"pending renewal on active subscription", "00000000-0000-0000-0000-000000000a81", "subscription", "pending", "active", 1},
		{"pending activation on past_due subscription", "00000000-0000-0000-0000-000000000a82", "activation", "pending", "past_due", 1},
		{"paid renewal", "00000000-0000-0000-0000-000000000a83", "subscription", "paid", "active", 0},
		{"superseded renewal", "00000000-0000-0000-0000-000000000a84", "subscription", "void", "active", 0},
		{"renewal failed by expiry", "00000000-0000-0000-0000-000000000a85", "subscription", "failed", "expired", 0},
		{"pending extension", "00000000-0000-0000-0000-000000000a86", "extension", "pending", "active", 0},
		{"pending addon increase", "00000000-0000-0000-0000-000000000a87", "addon_increase", "pending", "active", 0},
		{"cancelled subscription", "00000000-0000-0000-0000-000000000a88", "subscription", "pending", "cancelled", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testPoolBilling(t)
			setupBillingTest(t, pool, tc.orgID)
			invID := seedDunningCase(t, pool, tc.orgID, tc.subStatus, tc.kind, tc.invStatus)

			mod := billing.NewModuleForTest(pool, nil)
			if err := mod.Worker.HandleSubscriptionPaymentRemind(t.Context(), encodeInvoiceFailedBody(t, tc.orgID, invID)); err != nil {
				t.Fatalf("HandleSubscriptionPaymentRemind: %v", err)
			}
			if got := countOrgOutboxRows(t, pool, events.RoutingKeyInvoicePaymentReminder, tc.orgID); got != tc.want {
				t.Errorf("outbox %s: want %d, got %d", events.RoutingKeyInvoicePaymentReminder, tc.want, got)
			}
		})
	}
}

// TestIntegration_DunningNotice_FinalUsesFinalKey covers the day-7 handler:
// it applies the same gate but forwards under the final-notice key, carrying
// the scheduled row's invoice, and never under the day-3 key.
func TestIntegration_DunningNotice_FinalUsesFinalKey(t *testing.T) {
	pool := testPoolBilling(t)
	const orgID = "00000000-0000-0000-0000-000000000a89"
	setupBillingTest(t, pool, orgID)
	invID := seedDunningCase(t, pool, orgID, "active", "subscription", "pending")

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleSubscriptionPaymentFinal(t.Context(), encodeInvoiceFailedBody(t, orgID, invID)); err != nil {
		t.Fatalf("HandleSubscriptionPaymentFinal: %v", err)
	}
	if got := countOrgOutboxRows(t, pool, events.RoutingKeyInvoicePaymentFinalNotice, orgID); got != 1 {
		t.Errorf("outbox %s: want 1, got %d", events.RoutingKeyInvoicePaymentFinalNotice, got)
	}
	if got := countOrgOutboxRows(t, pool, events.RoutingKeyInvoicePaymentReminder, orgID); got != 0 {
		t.Errorf("outbox %s: want 0, got %d", events.RoutingKeyInvoicePaymentReminder, got)
	}
	var forwarded string
	if err := pool.QueryRow(t.Context(), `
		SELECT payload->'data'->>'invoice_id' FROM messaging.outbox
		WHERE routing_key = $1 AND payload->>'org_id' = $2`,
		events.RoutingKeyInvoicePaymentFinalNotice, orgID).Scan(&forwarded); err != nil {
		t.Fatalf("read forwarded notice: %v", err)
	}
	if forwarded != invID {
		t.Errorf("forwarded notice: want invoice_id %s, got %s", invID, forwarded)
	}
}

// TestIntegration_DunningNotice_UnknownInvoice_NoError covers a scheduled
// notice whose invoice no longer exists: dropped without an error, so it is
// acked rather than retried into the dead-letter queue.
func TestIntegration_DunningNotice_UnknownInvoice_NoError(t *testing.T) {
	pool := testPoolBilling(t)
	const orgID = "00000000-0000-0000-0000-000000000a8a"
	setupBillingTest(t, pool, orgID)

	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleSubscriptionPaymentRemind(t.Context(), encodeInvoiceFailedBody(t, orgID, uuid.New().String())); err != nil {
		t.Fatalf("HandleSubscriptionPaymentRemind: want nil for an unknown invoice, got %v", err)
	}
	if got := countOrgOutboxRows(t, pool, events.RoutingKeyInvoicePaymentReminder, orgID); got != 0 {
		t.Errorf("outbox %s: want 0, got %d", events.RoutingKeyInvoicePaymentReminder, got)
	}
}
