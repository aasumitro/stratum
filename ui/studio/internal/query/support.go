package query

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The app's billing outbox vocabulary (contracts/events in the API repo):
// rows Studio writes are relayed to the billing exchange and consumed by the
// billing worker like any event the app enqueues itself.
const (
	outboxExchangeBilling   = "billing.events"
	routingKeyMarkPaid      = "billing.invoice.mark-paid"
	routingKeyCheckoutClose = "billing.payment-link.expire"
	routingKeyTrialExtend   = "billing.subscription.operator-trial-extend"
	routingKeyPlanChange    = "billing.subscription.operator-plan-change"
)

type SupportUser struct {
	AuthSub    string
	FullName   string
	Email      string
	AvatarURL  string
	LastSeenAt *time.Time
}

type SupportOrganization struct {
	ID            string
	Name          string
	Slug          string
	Status        string
	Role          string
	Plan          string
	PlanName      string
	BillingStatus string
	TrialEnd      *time.Time
	PeriodEnd     *time.Time
}

type SupportInvoice struct {
	ID          string
	AmountCents int64
	Currency    string
	Status      string
	CreatedAt   time.Time
	PaidAt      *time.Time
}

func SearchSupportUsers(ctx context.Context, pool *pgxpool.Pool, q string) ([]SupportUser, error) {
	rows, err := pool.Query(ctx, `
		SELECT auth_sub, COALESCE(full_name,''), COALESCE(email,''),
		       COALESCE(avatar_url,''), last_seen_at
		FROM account.users
		WHERE $1 = '' OR email ILIKE '%' || $1 || '%'
		   OR full_name ILIKE '%' || $1 || '%' OR auth_sub = $1
		ORDER BY last_seen_at DESC NULLS LAST
		LIMIT 20
	`, q)
	if err != nil {
		return nil, fmt.Errorf("query.SearchSupportUsers: %w", err)
	}
	defer rows.Close()

	var out []SupportUser
	for rows.Next() {
		var u SupportUser
		if err := rows.Scan(&u.AuthSub, &u.FullName, &u.Email, &u.AvatarURL, &u.LastSeenAt); err != nil {
			return nil, fmt.Errorf("query.SearchSupportUsers: scan: %w", err)
		}
		out = append(out, u)
	}
	return out, nil
}

func GetSupportUserOrganizations(ctx context.Context, pool *pgxpool.Pool, authSub string) ([]SupportOrganization, error) {
	rows, err := pool.Query(ctx, `
		SELECT
			t.id, t.name, t.slug, t.status,
			m.role,
			COALESCE(s.plan, '')        AS plan,
			COALESCE(p.name, '')        AS plan_name,
			COALESCE(s.status, '')      AS billing_status,
			s.trial_end,
			s.period_end
		FROM organization.organizations t
		JOIN organization.memberships m ON m.organization_id = t.id AND m.auth_sub = $1
		LEFT JOIN billing.subscriptions s ON s.subject_id = t.id::text
		LEFT JOIN billing.plans p ON p.id = s.plan
		WHERE t.status != 'deleted'
		ORDER BY m.joined_at DESC
	`, authSub)
	if err != nil {
		return nil, fmt.Errorf("query.GetSupportUserOrganizations: %w", err)
	}
	defer rows.Close()

	var out []SupportOrganization
	for rows.Next() {
		var o SupportOrganization
		if err := rows.Scan(
			&o.ID, &o.Name, &o.Slug, &o.Status,
			&o.Role, &o.Plan, &o.PlanName, &o.BillingStatus,
			&o.TrialEnd, &o.PeriodEnd,
		); err != nil {
			return nil, fmt.Errorf("query.GetSupportUserOrganizations: scan: %w", err)
		}
		out = append(out, o)
	}
	return out, nil
}

type SupportLoginEvent struct {
	IPAddress string
	UserAgent string
	CreatedAt time.Time
}

// ListSupportUserLoginEvents returns a user's most recent login events,
// newest first — same table the account API's own GET /me/sessions reads,
// queried directly since Studio has no API layer.
func ListSupportUserLoginEvents(ctx context.Context, pool *pgxpool.Pool, authSub string, limit int) ([]SupportLoginEvent, error) {
	rows, err := pool.Query(ctx, `
		SELECT ip_address, user_agent, created_at
		FROM account.login_events
		WHERE auth_sub = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, authSub, limit)
	if err != nil {
		return nil, fmt.Errorf("query.ListSupportUserLoginEvents: %w", err)
	}
	defer rows.Close()

	var out []SupportLoginEvent
	for rows.Next() {
		var e SupportLoginEvent
		if err := rows.Scan(&e.IPAddress, &e.UserAgent, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("query.ListSupportUserLoginEvents: scan: %w", err)
		}
		out = append(out, e)
	}
	return out, nil
}

func ListSupportOrganizationInvoices(ctx context.Context, pool *pgxpool.Pool, organizationID string) ([]SupportInvoice, error) {
	rows, err := pool.Query(ctx, `
		SELECT i.id, i.amount_cents, i.currency, i.status, i.created_at, i.paid_at
		FROM billing.invoices i
		JOIN billing.subscriptions s ON s.id = i.subscription_id
		WHERE s.subject_id = $1
		ORDER BY i.created_at DESC
		LIMIT 10
	`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("query.ListSupportOrganizationInvoices: %w", err)
	}
	defer rows.Close()

	var out []SupportInvoice
	for rows.Next() {
		var inv SupportInvoice
		if err := rows.Scan(&inv.ID, &inv.AmountCents, &inv.Currency, &inv.Status, &inv.CreatedAt, &inv.PaidAt); err != nil {
			return nil, fmt.Errorf("query.ListSupportOrganizationInvoices: scan: %w", err)
		}
		out = append(out, inv)
	}
	return out, nil
}

// SupportExtendTrial asks the app's billing worker to move a trialing
// subscription's trial end by days. Billing moves the period with it and
// reschedules the conversion invoice, its reminder and the expiry check, which
// all key on the end. The new end is computed here, so a redelivered request
// extends nothing twice.
func SupportExtendTrial(ctx context.Context, pool *pgxpool.Pool, organizationID string, days int) error {
	tag, err := pool.Exec(ctx, `
		WITH target AS (
			SELECT gen_random_uuid() AS id, s.subject_id,
			       s.trial_end + make_interval(days => $2) AS trial_end
			FROM billing.subscriptions s
			WHERE s.subject_id = $1 AND s.status = 'trialing' AND s.trial_end IS NOT NULL
		)
		INSERT INTO messaging.outbox (id, exchange, routing_key, payload)
		SELECT t.id, $3::text, $4::text,
		       jsonb_build_object('id', t.id, 'type', $4::text, 'source', 'studio', 'time', now(),
		                          'org_id', t.subject_id,
		                          'data', jsonb_build_object('org_id', t.subject_id, 'trial_end', t.trial_end))
		FROM target t
	`, organizationID, days, outboxExchangeBilling, routingKeyTrialExtend)
	if err != nil {
		return fmt.Errorf("query.SupportExtendTrial: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("no trialing subscription found for organization %s", organizationID)
	}
	return nil
}

// SupportActivateSubscription marks a trialing or past_due subscription
// active for its current period. A cancelled one is left out: it only exists
// once its period has ended and the organization is suspended, so it comes
// back through the customer's own Resume and payment.
func SupportActivateSubscription(ctx context.Context, pool *pgxpool.Pool, organizationID string) error {
	tag, err := pool.Exec(ctx, `
		UPDATE billing.subscriptions
		SET status = 'active', trial_end = NULL
		WHERE subject_id = $1 AND status IN ('trialing', 'past_due')
	`, organizationID)
	if err != nil {
		return fmt.Errorf("query.SupportActivateSubscription: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("no subscription eligible for activation found for organization %s", organizationID)
	}
	return nil
}

// SupportChangePlan asks the app's billing worker to change an organization's
// plan through the same path an owner's change takes, so pending invoices are
// repriced and renewal follows a moved end. Billing applies it moments later.
func SupportChangePlan(ctx context.Context, pool *pgxpool.Pool, organizationID, plan, cycle string) error {
	var planExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM billing.plans WHERE id = $1)
	`, plan).Scan(&planExists); err != nil {
		return fmt.Errorf("query.SupportChangePlan: %w", err)
	}
	if !planExists {
		return fmt.Errorf("plan %q does not exist", plan)
	}
	tag, err := pool.Exec(ctx, `
		WITH target AS (
			SELECT gen_random_uuid() AS id, s.subject_id
			FROM billing.subscriptions s
			WHERE s.subject_id = $1
		)
		INSERT INTO messaging.outbox (id, exchange, routing_key, payload)
		SELECT t.id, $4::text, $5::text,
		       jsonb_build_object('id', t.id, 'type', $5::text, 'source', 'studio', 'time', now(),
		                          'org_id', t.subject_id,
		                          'data', jsonb_build_object('org_id', t.subject_id, 'plan', $2::text, 'cycle', $3::text))
		FROM target t
	`, organizationID, plan, cycle, outboxExchangeBilling, routingKeyPlanChange)
	if err != nil {
		return fmt.Errorf("query.SupportChangePlan: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("no subscription found for organization %s", organizationID)
	}
	return nil
}

type AtRiskInvoice struct {
	InvoiceID        string
	OrganizationID   string
	OrganizationName string
	OrganizationSlug string
	AmountCents      int64
	Currency         string
	Status           string
	CreatedAt        time.Time
	DaysPending      int
}

func GetAtRiskInvoices(ctx context.Context, pool *pgxpool.Pool) ([]AtRiskInvoice, error) {
	rows, err := pool.Query(ctx, `
		SELECT i.id,
		       t.id::text AS organization_id, t.name AS organization_name, t.slug AS organization_slug,
		       i.amount_cents, i.currency, i.status, i.created_at,
		       EXTRACT(DAY FROM now() - i.created_at)::int AS days_pending
		FROM billing.invoices i
		JOIN billing.subscriptions s ON s.id = i.subscription_id
		JOIN organization.organizations t ON t.id::text = s.subject_id
		WHERE i.status IN ('pending', 'failed')
		  AND (i.status = 'failed' OR i.created_at < now() - INTERVAL '7 days')
		ORDER BY i.status DESC, i.created_at ASC
		LIMIT 200
	`)
	if err != nil {
		return nil, fmt.Errorf("query.GetAtRiskInvoices: %w", err)
	}
	defer rows.Close()

	var out []AtRiskInvoice
	for rows.Next() {
		var inv AtRiskInvoice
		if err := rows.Scan(
			&inv.InvoiceID,
			&inv.OrganizationID, &inv.OrganizationName, &inv.OrganizationSlug,
			&inv.AmountCents, &inv.Currency, &inv.Status, &inv.CreatedAt,
			&inv.DaysPending,
		); err != nil {
			return nil, fmt.Errorf("query.GetAtRiskInvoices: scan: %w", err)
		}
		out = append(out, inv)
	}
	return out, nil
}

// SupportMarkInvoicePaid hands an operator's "mark paid" to the app's
// billing worker, which applies it exactly like a confirmed payment: the
// invoice turns paid, the subscription renews or reactivates, a billing
// suspension lifts and the next renewal is scheduled. Studio only writes the
// outbox row for a still-pending invoice; the invoice changes once the
// worker has run.
func SupportMarkInvoicePaid(ctx context.Context, pool *pgxpool.Pool, invoiceID string) error {
	tag, err := pool.Exec(ctx, `
		WITH target AS (
			SELECT gen_random_uuid() AS id, i.id AS invoice_id, s.subject_id
			FROM billing.invoices i
			JOIN billing.subscriptions s ON s.id = i.subscription_id
			WHERE i.id = $1 AND i.status = 'pending'
		)
		INSERT INTO messaging.outbox (id, exchange, routing_key, payload)
		SELECT t.id, $2::text, $3::text,
		       jsonb_build_object('id', t.id, 'type', $3::text, 'source', 'studio', 'time', now(),
		                          'org_id', t.subject_id, 'data', jsonb_build_object('invoice_id', t.invoice_id))
		FROM target t
	`, invoiceID, outboxExchangeBilling, routingKeyMarkPaid)
	if err != nil {
		return fmt.Errorf("query.SupportMarkInvoicePaid: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("invoice %s not found or not in pending status", invoiceID)
	}
	return nil
}

// SupportVoidInvoice voids a pending invoice, expires its open payment links
// and queues closing each link's checkout at its provider (the app's billing
// worker calls Stripe/Xendit), all in one transaction — a voided invoice must
// not stay payable on a checkout the provider still has open.
func SupportVoidInvoice(ctx context.Context, pool *pgxpool.Pool, invoiceID string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("query.SupportVoidInvoice: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orgID, subscriptionID string
	err = tx.QueryRow(ctx, `
		UPDATE billing.invoices i
		SET status = 'void', updated_at = now()
		FROM billing.subscriptions s
		WHERE i.id = $1 AND i.status = 'pending' AND s.id = i.subscription_id
		RETURNING s.subject_id, s.id::text
	`, invoiceID).Scan(&orgID, &subscriptionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("invoice %s not found or not in pending status", invoiceID)
	}
	if err != nil {
		return fmt.Errorf("query.SupportVoidInvoice: %w", err)
	}
	// An add-on increase gated on this invoice no longer waits for anything, as
	// when the app voids it.
	if _, err := tx.Exec(ctx, `
		UPDATE billing.subscription_addons
		SET pending_quantity = NULL, pending_invoice_id = NULL
		WHERE pending_invoice_id = $1
	`, invoiceID); err != nil {
		return fmt.Errorf("query.SupportVoidInvoice: clear pending add-on: %w", err)
	}
	// A voided invoice used no coupon application, so its discount lines give
	// theirs back, as the app's own void does. The app names a discount line
	// "Discount: <coupon code>".
	if _, err := tx.Exec(ctx, `
		UPDATE billing.coupon_redemptions cr
		SET applied_count = GREATEST(cr.applied_count - used.invoices, 0)
		FROM (
			SELECT li.description, COUNT(*) AS invoices
			FROM billing.invoice_line_items li
			WHERE li.invoice_id = $1 AND li.total_cents < 0
			GROUP BY li.description
		) used
		WHERE cr.subscription_id = $2::uuid AND used.description = 'Discount: ' || cr.coupon_id
	`, invoiceID, subscriptionID); err != nil {
		return fmt.Errorf("query.SupportVoidInvoice: release coupon application: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		WITH closed AS (
			UPDATE billing.payment_links SET status = 'expired'
			WHERE invoice_id = $1 AND status IN ('pending', 'failed')
			RETURNING provider, external_id
		), rows AS (
			SELECT gen_random_uuid() AS id, provider, external_id
			FROM closed
			WHERE external_id IS NOT NULL AND external_id <> ''
		)
		INSERT INTO messaging.outbox (id, exchange, routing_key, payload)
		SELECT r.id, $3::text, $4::text,
		       jsonb_build_object('id', r.id, 'type', $4::text, 'source', 'studio', 'time', now(),
		                          'org_id', $2::text,
		                          'data', jsonb_build_object('provider', r.provider, 'external_id', r.external_id))
		FROM rows r
	`, invoiceID, orgID, outboxExchangeBilling, routingKeyCheckoutClose); err != nil {
		return fmt.Errorf("query.SupportVoidInvoice: close payment links: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("query.SupportVoidInvoice: %w", err)
	}
	return nil
}

// PaymentToReview is money a provider took that settled nothing: a payment on
// an invoice already void, failed or paid. The app records it as unapplied and
// applies nothing, since there is no refund path; an operator refunds it at
// the provider and then marks it refunded.
type PaymentToReview struct {
	PaymentID        string
	InvoiceID        string
	InvoiceStatus    string
	OrganizationID   string
	OrganizationName string
	OrganizationSlug string
	AmountCents      int64
	Currency         string
	Provider         string
	ExternalID       string
	PaidAt           time.Time
}

func GetPaymentsToReview(ctx context.Context, pool *pgxpool.Pool) ([]PaymentToReview, error) {
	rows, err := pool.Query(ctx, `
		SELECT p.id::text, i.id::text, i.status,
		       s.subject_id, COALESCE(t.name, ''), COALESCE(t.slug, ''),
		       p.amount_cents, p.currency, p.provider, COALESCE(p.external_id, ''), p.paid_at
		FROM billing.payments p
		JOIN billing.invoices i ON i.id = p.invoice_id
		JOIN billing.subscriptions s ON s.id = i.subscription_id
		LEFT JOIN organization.organizations t ON t.id::text = s.subject_id
		WHERE p.status = 'completed' AND p.unapplied
		ORDER BY p.paid_at DESC
		LIMIT 200
	`)
	if err != nil {
		return nil, fmt.Errorf("query.GetPaymentsToReview: %w", err)
	}
	defer rows.Close()

	var out []PaymentToReview
	for rows.Next() {
		var p PaymentToReview
		if err := rows.Scan(
			&p.PaymentID, &p.InvoiceID, &p.InvoiceStatus,
			&p.OrganizationID, &p.OrganizationName, &p.OrganizationSlug,
			&p.AmountCents, &p.Currency, &p.Provider, &p.ExternalID, &p.PaidAt,
		); err != nil {
			return nil, fmt.Errorf("query.GetPaymentsToReview: scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query.GetPaymentsToReview: %w", err)
	}
	return out, nil
}

// SupportMarkPaymentRefunded records that an operator returned a payment at
// its provider. Only an unapplied payment still listed for review can be marked.
func SupportMarkPaymentRefunded(ctx context.Context, pool *pgxpool.Pool, paymentID string) error {
	tag, err := pool.Exec(ctx, `
		UPDATE billing.payments
		SET status = 'refunded'
		WHERE id = $1 AND status = 'completed' AND unapplied
	`, paymentID)
	if err != nil {
		return fmt.Errorf("query.SupportMarkPaymentRefunded: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("payment %s not found or not waiting for review", paymentID)
	}
	return nil
}
