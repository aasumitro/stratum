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

// The renewal reminder deliberately uses two keys. RoutingKeySubscriptionRemind
// is the scheduled check (a delayed outbox row due shortly before the period
// or trial ends) and only billing consumes it: the handler drops it when the
// subscription is no longer active/trialing or its end date moved, and
// otherwise publishes RoutingKeySubscriptionExpiringSoon, which notification
// sends. Billing's reminder queue binds RoutingKeySubscriptionRemind, so
// publishing the checked reminder under that key would feed the handler its
// own output on every relay tick; any other module binding it would receive
// unchecked, possibly stale reminders.
const RoutingKeySubscriptionExpiringSoon = "billing.subscription.expiring-soon"

// The day-3/day-7 dunning notices deliberately use two keys each, like the
// renewal reminder. RoutingKeySubscriptionPaymentRemind / -Final are the
// scheduled rows (delayed outbox rows written when a payment fails) and only
// billing consumes them: the handler drops a notice whose invoice is no
// longer pending, is not period-backing, or whose subscription has ended,
// and otherwise publishes RoutingKeyInvoicePaymentReminder /
// RoutingKeyInvoicePaymentFinalNotice, which notification sends.
const (
	RoutingKeyInvoicePaymentReminder    = "billing.invoice.payment-reminder"
	RoutingKeyInvoicePaymentFinalNotice = "billing.invoice.payment-final-notice"
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
