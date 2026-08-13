package contracts

import "context"

// SubscriptionInfo is the minimal projection of subscription data for
// modules that need to gate features by plan without owning billing tables.
type SubscriptionInfo struct {
	ID          string
	SubjectType string // "organization" | "user"
	SubjectID   string
	Plan        string // "solo" | "growth" | "custom"
	Status      string // "active" | "trialing" | "cancelled" | "past_due" | "expired"
	Cycle       string // "monthly" | "yearly"
	Currency    string // "USD" | "IDR"
}

// BillingReader is implemented by the billing module. Consume it when
// gating a feature behind a subscription plan check.
//
// Every method here can be called with no ambient database transaction in
// context (a background goroutine, not an HTTP request behind the RLS
// middleware) — and billing.subscriptions/invoices/payments/payment_links
// carry FORCE ROW LEVEL SECURITY, which silently returns zero rows for any
// query that isn't wrapped in a transaction with the org context set. A new
// method that reads one of those tables must dispatch through
// db.HasQuerier + withOrgTx (see billing's checkUsageLimit/checkFeatureAccess
// for the pattern), not just the module's own querier(ctx) fallback — the
// difference is invisible in a request-path test and only shows up as
// requests silently failing once the code runs from a caller with no
// ambient transaction — this exact gap has already been found and fixed
// independently in more than one method here, each time by testing a
// background caller directly rather than by code review alone.
type BillingReader interface {
	GetSubscriptionBySubject(ctx context.Context, subjectType, subjectID string) (*SubscriptionInfo, error)
	CheckUsageLimit(ctx context.Context, organizationID, metric string) (current int64, limit int, err error)
	CheckFeatureAccess(ctx context.Context, organizationID, feature string) error
}

// BillingWriter is implemented by the billing module. The organization module
// calls RecordUsage after any billable operation succeeds to keep usage
// counters in sync with the actual state of the database.
//
// RecordUsage carries the same FORCE-RLS-without-ambient-transaction risk
// BillingReader's doc comment describes — see there before adding a method
// here that touches subscriptions/invoices/payments/payment_links.
type BillingWriter interface {
	RecordUsage(ctx context.Context, organizationID, metric string, value int64) error

	// AnonymizeHistory replaces changed_by with a placeholder across a
	// user's subscription history entries — part of the GDPR
	// account-deletion flow.
	AnonymizeHistory(ctx context.Context, authSub string) error
}
