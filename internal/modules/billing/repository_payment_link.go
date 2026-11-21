package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
)

type paymentLinkRecord struct {
	ID          string     `json:"id"`
	InvoiceID   string     `json:"invoice_id"`
	Provider    string     `json:"provider"`
	Currency    string     `json:"currency"`
	AmountCents int64      `json:"amount_cents"`
	ExternalID  *string    `json:"external_id,omitempty"`
	URL         *string    `json:"url,omitempty"`
	Status      string     `json:"status"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// paymentLinkSubject is a minimal projection for webhook processing.
type paymentLinkSubject struct {
	linkID         string
	invoiceID      string
	subscriptionID string
	status         string
	subjectType    string
	subjectID      string
}

func (r *repository) insertPaymentLink(
	ctx context.Context, q db.Querier,
	id, invoiceID, provider, currency string,
	amountCents int64,
	externalID, url *string,
	expiresAt *time.Time,
) (*paymentLinkRecord, error) {
	var pl paymentLinkRecord
	// Inserted only while the invoice is still pending: it can be voided while
	// the provider call that created this checkout was in flight, and its void
	// expired only the links that existed then. FOR SHARE makes a void racing
	// this insert either finish first (and the row is skipped) or wait for it
	// (and see the link). No row means pgx.ErrNoRows.
	err := q.QueryRow(ctx, `
		INSERT INTO billing.payment_links
		    (id, invoice_id, provider, currency, amount_cents, external_id, url, expires_at)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8
		WHERE EXISTS (SELECT 1 FROM billing.invoices WHERE id = $2 AND status = 'pending' FOR SHARE)
		RETURNING id, invoice_id, provider, currency, amount_cents,
		          external_id, url, status, expires_at, created_at`,
		id, invoiceID, provider, currency, amountCents, externalID, url, expiresAt,
	).Scan(&pl.ID, &pl.InvoiceID, &pl.Provider, &pl.Currency, &pl.AmountCents,
		&pl.ExternalID, &pl.URL, &pl.Status, &pl.ExpiresAt, &pl.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("billing.insertPaymentLink: %w", err)
	}
	return &pl, nil
}

func (r *repository) findPaymentLinkByExternalID(
	ctx context.Context, q db.Querier,
	externalID string,
) (*paymentLinkRecord, error) {
	var pl paymentLinkRecord
	err := q.QueryRow(ctx, `
		SELECT id, invoice_id, provider, currency, amount_cents,
		       external_id, url, status, expires_at, created_at
		FROM billing.payment_links WHERE external_id = $1`,
		externalID,
	).Scan(&pl.ID, &pl.InvoiceID, &pl.Provider, &pl.Currency, &pl.AmountCents,
		&pl.ExternalID, &pl.URL, &pl.Status, &pl.ExpiresAt, &pl.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("billing.findPaymentLinkByExternalID: %w", err)
	}
	return &pl, nil
}

// findPaymentLinkWithSubjectByExternalID joins payment_links → invoices → subscriptions
// to resolve the billing subject in a single query, avoiding N+1 in webhook handlers.
func (r *repository) findPaymentLinkWithSubjectByExternalID(
	ctx context.Context, q db.Querier,
	externalID string,
) (*paymentLinkSubject, error) {
	var p paymentLinkSubject
	err := q.QueryRow(ctx, `
		SELECT pl.id, pl.invoice_id, s.id, pl.status, s.subject_type, s.subject_id
		FROM billing.payment_links pl
		JOIN billing.invoices i ON i.id = pl.invoice_id
		JOIN billing.subscriptions s ON s.id = i.subscription_id
		WHERE pl.external_id = $1`,
		externalID,
	).Scan(&p.linkID, &p.invoiceID, &p.subscriptionID, &p.status, &p.subjectType, &p.subjectID)
	if err != nil {
		return nil, fmt.Errorf("billing.findPaymentLinkWithSubjectByExternalID: %w", err)
	}
	return &p, nil
}

// findPaymentLinkWithSubjectByInvoiceID is findPaymentLinkWithSubjectByExternalID's
// counterpart keyed by invoice_id instead of external_id — used for Stripe
// event types whose data object is a PaymentIntent (payment_intent.*),
// which carries a different ID namespace (pi_...) than the Checkout
// Session ID (cs_...) stored in external_id, so external_id can never
// match for those events. linkID, carried in the same metadata, names the checkout
// the event came from. A checkout created before that metadata existed has none, and
// then the most recent link wins (mirrors findActivePaymentLinkByInvoice's ordering).
func (r *repository) findPaymentLinkWithSubjectByInvoiceID(
	ctx context.Context, q db.Querier,
	invoiceID, linkID string,
) (*paymentLinkSubject, error) {
	var p paymentLinkSubject
	err := q.QueryRow(ctx, `
		SELECT pl.id, pl.invoice_id, s.id, pl.status, s.subject_type, s.subject_id
		FROM billing.payment_links pl
		JOIN billing.invoices i ON i.id = pl.invoice_id
		JOIN billing.subscriptions s ON s.id = i.subscription_id
		WHERE pl.invoice_id = $1 AND ($2 = '' OR pl.id::text = $2)
		ORDER BY pl.created_at DESC LIMIT 1`,
		invoiceID, linkID,
	).Scan(&p.linkID, &p.invoiceID, &p.subscriptionID, &p.status, &p.subjectType, &p.subjectID)
	if err != nil {
		return nil, fmt.Errorf("billing.findPaymentLinkWithSubjectByInvoiceID: %w", err)
	}
	return &p, nil
}

func (r *repository) listActivePaymentLinks(
	ctx context.Context, q db.Querier,
	subscriptionID string,
) ([]paymentLinkRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT pl.id, pl.invoice_id, pl.provider, pl.currency, pl.amount_cents,
		       pl.external_id, pl.url, pl.status, pl.expires_at, pl.created_at
		FROM billing.payment_links pl
		JOIN billing.invoices i ON i.id = pl.invoice_id
		WHERE i.subscription_id = $1 AND pl.status = 'pending'
		  AND (pl.expires_at IS NULL OR pl.expires_at > now())
		ORDER BY pl.created_at DESC`,
		subscriptionID,
	)
	if err != nil {
		return nil, fmt.Errorf("billing.listActivePaymentLinks: %w", err)
	}
	defer rows.Close()

	var out []paymentLinkRecord
	for rows.Next() {
		var pl paymentLinkRecord
		if err := rows.Scan(
			&pl.ID, &pl.InvoiceID, &pl.Provider, &pl.Currency, &pl.AmountCents,
			&pl.ExternalID, &pl.URL, &pl.Status, &pl.ExpiresAt, &pl.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("billing.listActivePaymentLinks: scan: %w", err)
		}
		out = append(out, pl)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing.listActivePaymentLinks: %w", err)
	}
	return out, nil
}

func (r *repository) findActivePaymentLinkByInvoice(
	ctx context.Context, q db.Querier,
	invoiceID string,
) (*paymentLinkRecord, error) {
	var pl paymentLinkRecord
	err := q.QueryRow(ctx, `
		SELECT id, invoice_id, provider, currency, amount_cents,
		       external_id, url, status, expires_at, created_at
		FROM billing.payment_links
		WHERE invoice_id = $1 AND status = 'pending'
		  AND (expires_at IS NULL OR expires_at > now())
		ORDER BY created_at DESC LIMIT 1`,
		invoiceID,
	).Scan(&pl.ID, &pl.InvoiceID, &pl.Provider, &pl.Currency, &pl.AmountCents,
		&pl.ExternalID, &pl.URL, &pl.Status, &pl.ExpiresAt, &pl.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("billing.findActivePaymentLinkByInvoice: %w", err)
	}
	return &pl, nil
}

// closedLink is a payment link that was still open — pending, or failed
// and so still retryable on its hosted page — when it was expired locally.
// Its checkout may still be open at the provider until closed there too
// (enqueueCheckoutExpiry).
type closedLink struct {
	Provider   string
	ExternalID string
}

// queryClosedLinks runs an UPDATE … RETURNING provider, external_id that
// expires payment links, collecting the links that had a provider-side
// checkout to close (external_id set).
func queryClosedLinks(ctx context.Context, q db.Querier, sql string, args ...any) ([]closedLink, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var links []closedLink
	for rows.Next() {
		var provider string
		var externalID *string
		if err := rows.Scan(&provider, &externalID); err != nil {
			return nil, err
		}
		if externalID != nil && *externalID != "" {
			links = append(links, closedLink{Provider: provider, ExternalID: *externalID})
		}
	}
	return links, rows.Err()
}

// expireOpenPaymentLinks expires every open link of the invoice — before a
// new one is created, or once the invoice is paid — returning the links
// whose checkout must close at the provider too, so no second checkout can
// take the same payment.
func (r *repository) expireOpenPaymentLinks(ctx context.Context, q db.Querier, invoiceID string) ([]closedLink, error) {
	links, err := queryClosedLinks(ctx, q, `
		UPDATE billing.payment_links SET status = 'expired'
		WHERE invoice_id = $1 AND status IN ('pending', 'failed')
		RETURNING provider, external_id`,
		invoiceID,
	)
	if err != nil {
		return nil, fmt.Errorf("billing.expireOpenPaymentLinks: %w", err)
	}
	return links, nil
}

func (r *repository) updatePaymentLinkStatus(ctx context.Context, q db.Querier, id, status string) error {
	_, err := q.Exec(ctx, `
		UPDATE billing.payment_links SET status = $2 WHERE id = $1`,
		id, status,
	)
	if err != nil {
		return fmt.Errorf("billing.updatePaymentLinkStatus: %w", err)
	}
	return nil
}

// deleteExpiredWebhookEvents removes inbound replay markers older than days. A
// provider resending an event older than that is no longer deduped here; invoice
// status still stops it from applying twice.
func (r *repository) deleteExpiredWebhookEvents(ctx context.Context, q db.Querier, days int) (int64, error) {
	removed, err := db.SweepInBatches(ctx, q, `
		DELETE FROM billing.webhook_events
		WHERE (provider, event_id) IN (
			SELECT provider, event_id
			FROM billing.webhook_events
			WHERE received_at < now() - make_interval(days => $1)
			ORDER BY received_at
			LIMIT $2
		)`, days)
	if err != nil {
		return removed, fmt.Errorf("billing.deleteExpiredWebhookEvents: %w", err)
	}
	return removed, nil
}

// markWebhookProcessed inserts (provider, eventID) into billing.webhook_events.
// Returns true when the row was newly inserted, false when it already existed (duplicate delivery).
func (r *repository) markWebhookProcessed(ctx context.Context, q db.Querier, provider, eventID string) (bool, error) {
	tag, err := q.Exec(ctx,
		`INSERT INTO billing.webhook_events (provider, event_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		provider, eventID)
	if err != nil {
		return false, fmt.Errorf("billing.markWebhookProcessed: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
