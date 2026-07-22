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
type BillingReader interface {
	GetSubscriptionBySubject(ctx context.Context, subjectType, subjectID string) (*SubscriptionInfo, error)
	CheckUsageLimit(ctx context.Context, organizationID, metric string) (current int64, limit int, err error)
	CheckFeatureAccess(ctx context.Context, organizationID, feature string) error
}

// BillingWriter is implemented by the billing module. The organization module
// calls RecordUsage after any billable operation succeeds to keep usage
// counters in sync with the actual state of the database.
type BillingWriter interface {
	RecordUsage(ctx context.Context, organizationID, metric string, value int64) error

	// AnonymizeHistory replaces changed_by with a placeholder across a
	// user's subscription history entries — part of the GDPR
	// account-deletion flow.
	AnonymizeHistory(ctx context.Context, authSub string) error
}
