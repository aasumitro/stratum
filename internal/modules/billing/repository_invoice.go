package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
)

type invoiceRecord struct {
	ID             string  `json:"id"`
	SubscriptionID string  `json:"subscription_id"`
	InvoiceNumber  *string `json:"invoice_number,omitempty"`
	AmountCents    int64   `json:"amount_cents"`
	SubtotalCents  *int64  `json:"subtotal_cents,omitempty"`
	TaxRateBPS     int     `json:"tax_rate_bps"`
	TaxCents       int64   `json:"tax_cents"`
	Currency       string  `json:"currency"`
	Status         string  `json:"status"`
	Kind           string  `json:"kind"`
	SwitchToAnnual bool    `json:"switch_to_annual"`
	// ExtensionMonths is set only for kind="extension" invoices — the exact
	// months purchased, read back by applyExtensionPayment instead of
	// re-derived by summing line items (service_webhook.go).
	ExtensionMonths   *int       `json:"extension_months,omitempty"`
	ProviderInvoiceID *string    `json:"provider_invoice_id,omitempty"`
	DueAt             *time.Time `json:"due_at,omitempty"`
	PaidAt            *time.Time `json:"paid_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type lineItemRecord struct {
	ID             string `json:"id"`
	InvoiceID      string `json:"invoice_id"`
	Description    string `json:"description"`
	Quantity       int    `json:"quantity"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	TotalCents     int64  `json:"total_cents"`
	Currency       string `json:"currency"`
	SortOrder      int    `json:"sort_order"`
}

func (r *repository) listInvoices(
	ctx context.Context, q db.Querier,
	subscriptionID string,
) ([]invoiceRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT id, subscription_id, invoice_number, amount_cents, subtotal_cents, tax_rate_bps, tax_cents, currency, status,
		       kind, switch_to_annual, extension_months, provider_invoice_id, due_at, paid_at, created_at, updated_at
		FROM billing.invoices
		WHERE subscription_id = $1
		ORDER BY created_at DESC`,
		subscriptionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []invoiceRecord
	for rows.Next() {
		var inv invoiceRecord
		if err := rows.Scan(
			&inv.ID, &inv.SubscriptionID, &inv.InvoiceNumber, &inv.AmountCents,
			&inv.SubtotalCents, &inv.TaxRateBPS, &inv.TaxCents, &inv.Currency, &inv.Status, &inv.Kind,
			&inv.SwitchToAnnual, &inv.ExtensionMonths, &inv.ProviderInvoiceID, &inv.DueAt, &inv.PaidAt, &inv.CreatedAt, &inv.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (r *repository) findInvoiceByID(ctx context.Context, q db.Querier, id string) (*invoiceRecord, error) {
	inv := new(invoiceRecord)
	err := q.QueryRow(ctx, `
		SELECT id, subscription_id, invoice_number, amount_cents, subtotal_cents, tax_rate_bps, tax_cents, currency, status,
		       kind, switch_to_annual, extension_months, provider_invoice_id, due_at, paid_at, created_at, updated_at
		FROM billing.invoices WHERE id = $1`,
		id,
	).Scan(&inv.ID, &inv.SubscriptionID, &inv.InvoiceNumber, &inv.AmountCents,
		&inv.SubtotalCents, &inv.TaxRateBPS, &inv.TaxCents, &inv.Currency, &inv.Status, &inv.Kind,
		&inv.SwitchToAnnual, &inv.ExtensionMonths, &inv.ProviderInvoiceID, &inv.DueAt, &inv.PaidAt, &inv.CreatedAt, &inv.UpdatedAt)
	return inv, err
}

// findInvoiceByIDAndSubject ensures the invoice belongs to a subscription
// owned by the given subject. Prevents IDOR across organizations.
func (r *repository) findInvoiceByIDAndSubject(
	ctx context.Context, q db.Querier,
	invoiceID, subjectType, subjectID string,
) (*invoiceRecord, error) {
	inv := new(invoiceRecord)
	err := q.QueryRow(ctx, `
		SELECT i.id, i.subscription_id, i.invoice_number, i.amount_cents, i.subtotal_cents, i.tax_rate_bps, i.tax_cents,
		       i.currency, i.status, i.kind, i.switch_to_annual, i.extension_months, i.provider_invoice_id, i.due_at, i.paid_at, i.created_at, i.updated_at
		FROM billing.invoices i
		JOIN billing.subscriptions s ON s.id = i.subscription_id
		WHERE i.id = $1 AND s.subject_type = $2 AND s.subject_id = $3`,
		invoiceID, subjectType, subjectID,
	).Scan(&inv.ID, &inv.SubscriptionID, &inv.InvoiceNumber, &inv.AmountCents,
		&inv.SubtotalCents, &inv.TaxRateBPS, &inv.TaxCents, &inv.Currency, &inv.Status, &inv.Kind,
		&inv.SwitchToAnnual, &inv.ExtensionMonths, &inv.ProviderInvoiceID, &inv.DueAt, &inv.PaidAt, &inv.CreatedAt, &inv.UpdatedAt)
	return inv, err
}

// findPendingInvoiceBySubscription returns subscriptionID's pending
// invoice, or pgx.ErrNoRows if it has none. Used to recover a payment link
// that failed to generate after its invoice was already created.
func (r *repository) findPendingInvoiceBySubscription(ctx context.Context, q db.Querier, subscriptionID string) (*invoiceRecord, error) {
	inv := new(invoiceRecord)
	err := q.QueryRow(ctx, `
		SELECT id, subscription_id, invoice_number, amount_cents, subtotal_cents, tax_rate_bps, tax_cents,
		       currency, status, kind, switch_to_annual, extension_months, provider_invoice_id, due_at, paid_at, created_at, updated_at
		FROM billing.invoices
		WHERE subscription_id = $1 AND status = 'pending'
		ORDER BY created_at DESC LIMIT 1`,
		subscriptionID,
	).Scan(&inv.ID, &inv.SubscriptionID, &inv.InvoiceNumber, &inv.AmountCents,
		&inv.SubtotalCents, &inv.TaxRateBPS, &inv.TaxCents, &inv.Currency, &inv.Status, &inv.Kind,
		&inv.SwitchToAnnual, &inv.ExtensionMonths, &inv.ProviderInvoiceID, &inv.DueAt, &inv.PaidAt, &inv.CreatedAt, &inv.UpdatedAt)
	return inv, err
}

// markInvoicePaid transitions a pending invoice to paid. The WHERE clause
// makes this the idempotency boundary for the paid-webhook path: it returns
// false when the invoice was already paid, so a redelivered payment
// notification (a provider retry or a second event type for the same
// underlying payment, arriving under a different event ID than the first)
// is a no-op instead of re-applying the invoice's effects a second time.
func (r *repository) markInvoicePaid(ctx context.Context, q db.Querier, id string, paidAt time.Time) (bool, error) {
	tag, err := q.Exec(ctx, `
		UPDATE billing.invoices SET status = 'paid', paid_at = $2, updated_at = now()
		WHERE id = $1 AND status = 'pending'`,
		id, paidAt,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *repository) nextInvoiceNumber(ctx context.Context, q db.Querier, organizationID string, year int) (string, error) {
	var seq int
	err := q.QueryRow(ctx, `
		INSERT INTO billing.invoice_sequences (organization_id, year, last_seq)
		VALUES ($1, $2, 1)
		ON CONFLICT (organization_id, year) DO UPDATE
			SET last_seq = billing.invoice_sequences.last_seq + 1
		RETURNING last_seq`,
		organizationID, year,
	).Scan(&seq)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("INV-%d-%05d", year, seq), nil
}

// extensionMonths is nil for every invoice kind except "extension", where it
// records the exact months purchased — see invoiceRecord.ExtensionMonths.
func (r *repository) insertInvoice(
	ctx context.Context, q db.Querier,
	organizationID, subscriptionID string, subtotalCents int64,
	taxRateBPS int, taxCents int64, currency, kind string, switchToAnnual bool, extensionMonths *int,
) (*invoiceRecord, error) {
	var inv invoiceRecord
	dueAt := time.Now().AddDate(0, 0, 7)
	totalCents := subtotalCents + taxCents

	var invNum *string
	if organizationID != "" {
		n, err := r.nextInvoiceNumber(ctx, q, organizationID, time.Now().Year())
		if err == nil {
			invNum = &n
		}
	}

	err := q.QueryRow(ctx, `
		INSERT INTO billing.invoices (subscription_id, invoice_number, amount_cents, subtotal_cents, tax_rate_bps, tax_cents, currency, kind, switch_to_annual, extension_months, due_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, subscription_id, invoice_number, amount_cents, subtotal_cents, tax_rate_bps, tax_cents, currency, status,
		          kind, switch_to_annual, extension_months, provider_invoice_id, due_at, paid_at, created_at, updated_at`,
		subscriptionID, invNum, totalCents, subtotalCents, taxRateBPS, taxCents, currency, kind, switchToAnnual, extensionMonths, dueAt,
	).Scan(&inv.ID, &inv.SubscriptionID, &inv.InvoiceNumber, &inv.AmountCents,
		&inv.SubtotalCents, &inv.TaxRateBPS, &inv.TaxCents, &inv.Currency, &inv.Status, &inv.Kind,
		&inv.SwitchToAnnual, &inv.ExtensionMonths, &inv.ProviderInvoiceID, &inv.DueAt, &inv.PaidAt, &inv.CreatedAt, &inv.UpdatedAt)
	return &inv, err
}

func (r *repository) insertLineItem(
	ctx context.Context, q db.Querier,
	invoiceID, description, currency string, quantity int, unitPriceCents, totalCents int64, sortOrder int,
) error {
	_, err := q.Exec(ctx, `
		INSERT INTO billing.invoice_line_items (invoice_id, description, quantity, unit_price_cents, total_cents, currency, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		invoiceID, description, quantity, unitPriceCents, totalCents, currency, sortOrder,
	)
	return err
}

func (r *repository) listLineItems(ctx context.Context, q db.Querier, invoiceID string) ([]lineItemRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT id, invoice_id, description, quantity, unit_price_cents, total_cents, currency, sort_order
		FROM billing.invoice_line_items
		WHERE invoice_id = $1
		ORDER BY sort_order`,
		invoiceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []lineItemRecord
	for rows.Next() {
		var li lineItemRecord
		if err := rows.Scan(&li.ID, &li.InvoiceID, &li.Description, &li.Quantity, &li.UnitPriceCents, &li.TotalCents, &li.Currency, &li.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, li)
	}
	return out, rows.Err()
}

func (r *repository) hasPendingInvoice(ctx context.Context, q db.Querier, subscriptionID string) (bool, error) {
	var count int
	err := q.QueryRow(ctx, `
		SELECT COUNT(*) FROM billing.invoices
		WHERE subscription_id = $1 AND status = 'pending'`,
		subscriptionID,
	).Scan(&count)
	return count > 0, err
}

// hasPendingInvoiceBlockingExtend is hasPendingInvoice narrowed to exclude
// addon_increase invoices: an unpaid addon increase, like an unpaid
// extension, pays for something additional to the current period, so it
// doesn't need to be resolved before requesting more runway on that period
// — only an invoice that backs the period itself should block extending it.
func (r *repository) hasPendingInvoiceBlockingExtend(ctx context.Context, q db.Querier, subscriptionID string) (bool, error) {
	var count int
	err := q.QueryRow(ctx, `
		SELECT COUNT(*) FROM billing.invoices
		WHERE subscription_id = $1 AND status = 'pending' AND kind != 'addon_increase'`,
		subscriptionID,
	).Scan(&count)
	return count > 0, err
}

func (r *repository) voidPendingInvoicesAndLinks(ctx context.Context, q db.Querier, subscriptionID string) error {
	_, err := q.Exec(ctx, `
		WITH voided AS (
			UPDATE billing.invoices SET status = 'void', updated_at = now()
			WHERE subscription_id = $1 AND status = 'pending'
			RETURNING id
		)
		UPDATE billing.payment_links SET status = 'expired'
		WHERE invoice_id IN (SELECT id FROM voided)`,
		subscriptionID,
	)
	return err
}

// voidInvoiceAndLinks is voidPendingInvoicesAndLinks scoped to one invoice
// instead of every pending invoice on a subscription — used to supersede a
// single stale addon-increase invoice on a repeat request without touching
// an unrelated pending invoice (e.g. a renewal) on the same subscription.
func (r *repository) voidInvoiceAndLinks(ctx context.Context, q db.Querier, invoiceID string) error {
	_, err := q.Exec(ctx, `
		WITH voided AS (
			UPDATE billing.invoices SET status = 'void', updated_at = now()
			WHERE id = $1 AND status = 'pending'
			RETURNING id
		)
		UPDATE billing.payment_links SET status = 'expired'
		WHERE invoice_id IN (SELECT id FROM voided)`,
		invoiceID,
	)
	return err
}
