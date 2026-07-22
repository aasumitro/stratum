package billing

import (
	"context"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
)

type paymentRecord struct {
	ID          string    `json:"id"`
	InvoiceID   string    `json:"invoice_id"`
	AmountCents int64     `json:"amount_cents"`
	Currency    string    `json:"currency"`
	Provider    string    `json:"provider"`
	ExternalID  *string   `json:"external_id,omitempty"`
	Status      string    `json:"status"`
	PaidAt      time.Time `json:"paid_at"`
	CreatedAt   time.Time `json:"created_at"`
}

func (r *repository) insertPayment(
	ctx context.Context, q db.Querier,
	invoiceID string, amountCents int64, currency, provider string, externalID *string, paidAt time.Time,
) error {
	_, err := q.Exec(ctx, `
		INSERT INTO billing.payments (invoice_id, amount_cents, currency, provider, external_id, paid_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		invoiceID, amountCents, currency, provider, externalID, paidAt,
	)
	return err
}

func (r *repository) listPayments(
	ctx context.Context, q db.Querier, subscriptionID string,
) ([]paymentRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT p.id, p.invoice_id, p.amount_cents, p.currency, p.provider,
		       p.external_id, p.status, p.paid_at, p.created_at
		FROM billing.payments p
		JOIN billing.invoices i ON i.id = p.invoice_id
		WHERE i.subscription_id = $1
		ORDER BY p.paid_at DESC`,
		subscriptionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []paymentRecord
	for rows.Next() {
		var p paymentRecord
		if err := rows.Scan(
			&p.ID, &p.InvoiceID, &p.AmountCents, &p.Currency, &p.Provider,
			&p.ExternalID, &p.Status, &p.PaidAt, &p.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
