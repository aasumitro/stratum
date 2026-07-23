package events

import "time"

const (
	RoutingKeySubscriptionActivated     = "billing.subscription.activated"
	RoutingKeySubscriptionCancelled     = "billing.subscription.cancelled"
	RoutingKeySubscriptionExpired       = "billing.subscription.expired"
	RoutingKeyInvoicePaid               = "billing.invoice.paid"
	RoutingKeyInvoiceFailed             = "billing.invoice.failed"
	RoutingKeySubscriptionCheck         = "billing.subscription.check"
	RoutingKeySubscriptionResumed       = "billing.subscription.resumed"
	RoutingKeySubscriptionExtended      = "billing.subscription.extended"
	RoutingKeySubscriptionRemind        = "billing.subscription.remind"
	RoutingKeySubscriptionAutoInvoice   = "billing.subscription.auto-invoice"
	RoutingKeyInvoiceCreated            = "billing.invoice.created"
	RoutingKeySubscriptionPaymentRemind = "billing.subscription.payment-remind"
	RoutingKeySubscriptionPaymentFinal  = "billing.subscription.payment-final"
	RoutingKeyTrialStarted              = "billing.subscription.trial-started"
	RoutingKeyUsageLimitWarning         = "billing.usage.limit-warning"
)

// Delay-local routing keys — used only on the ExchangeBillingDelay parking
// queue before its per-message TTL expires and the dead-letter exchange
// re-publishes under the matching RoutingKeyXxx above (e.g.
// DelayRoutingKeySubscriptionRemind -> dead-letters to
// RoutingKeySubscriptionRemind). Deliberately distinct, shorter strings from
// the final routing keys. Referenced from both the publish side
// (billing/service.go) and the queue-declare side (internal/app/worker.go);
// previously hardcoded independently in both, with nothing catching a
// mismatch between them.
const (
	DelayRoutingKeySubscriptionCheck         = "subscription-check"
	DelayRoutingKeySubscriptionRemind        = "subscription-remind"
	DelayRoutingKeySubscriptionAutoInvoice   = "subscription-auto-invoice"
	DelayRoutingKeySubscriptionPaymentRemind = "subscription-payment-remind"
	DelayRoutingKeySubscriptionPaymentFinal  = "subscription-payment-final"
)

type SubscriptionActivated struct {
	OrgID          string    `json:"org_id"`
	SubscriptionID string    `json:"subscription_id"`
	Plan           string    `json:"plan"`
	ActivatedAt    time.Time `json:"activated_at"`
}

type SubscriptionCancelled struct {
	OrgID          string    `json:"org_id"`
	SubscriptionID string    `json:"subscription_id"`
	CancelledAt    time.Time `json:"cancelled_at"`
}

// SubscriptionExpired is published when a subscription's trial or paid
// period lapses unpaid (expireIfDue) — distinct from SubscriptionCancelled
// (an owner-initiated cancel, which stays active until period end): by the
// time this fires the organization has already been suspended.
type SubscriptionExpired struct {
	OrgID          string    `json:"org_id"`
	SubscriptionID string    `json:"subscription_id"`
	ExpiredAt      time.Time `json:"expired_at"`
}

type InvoicePaid struct {
	OrgID       string    `json:"org_id"`
	InvoiceID   string    `json:"invoice_id"`
	AmountCents int       `json:"amount_cents"`
	Currency    string    `json:"currency"`
	PaidAt      time.Time `json:"paid_at"`
}

type InvoiceFailed struct {
	OrgID     string    `json:"org_id"`
	InvoiceID string    `json:"invoice_id"`
	FailedAt  time.Time `json:"failed_at"`
}

type SubscriptionResumed struct {
	OrgID          string    `json:"org_id"`
	SubscriptionID string    `json:"subscription_id"`
	Plan           string    `json:"plan"`
	ResumedAt      time.Time `json:"resumed_at"`
}

type SubscriptionExtended struct {
	OrgID          string    `json:"org_id"`
	SubscriptionID string    `json:"subscription_id"`
	Plan           string    `json:"plan"`
	Months         int       `json:"months"`
	NewPeriodEnd   time.Time `json:"new_period_end"`
}

type InvoiceCreated struct {
	OrgID       string    `json:"org_id"`
	InvoiceID   string    `json:"invoice_id"`
	Plan        string    `json:"plan"`
	AmountCents int64     `json:"amount_cents"`
	Currency    string    `json:"currency"`
	DueAt       time.Time `json:"due_at"`
	// FromTrial is true when this invoice converts a trial to its first
	// paid period, rather than renewing an already-active subscription.
	FromTrial bool `json:"from_trial"`
}

type SubscriptionCheck struct {
	SubscriptionID string    `json:"subscription_id"`
	SubjectType    string    `json:"subject_type"`
	SubjectID      string    `json:"subject_id"`
	Plan           string    `json:"plan"`
	ExpectedEnd    time.Time `json:"expected_end"`
	// IsTrial is true when ExpectedEnd is a trial's end date rather than a
	// regular billing period's end date.
	IsTrial bool `json:"is_trial"`
}

// TrialStarted is published when a new organization's first-ever
// subscription is provisioned as a trial.
type TrialStarted struct {
	OrgID    string    `json:"org_id"`
	Plan     string    `json:"plan"`
	TrialEnd time.Time `json:"trial_end"`
}

// UsageLimitWarning is published the moment a metered feature's usage
// crosses 90% of its effective limit (plan + any attached addon delta) —
// fired once per crossing (checked against the pre-write value), not on
// every subsequent recordUsage call once already over the threshold.
type UsageLimitWarning struct {
	OrgID   string `json:"org_id"`
	Metric  string `json:"metric"`
	Current int64  `json:"current"`
	Limit   int    `json:"limit"`
}
