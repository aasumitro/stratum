package events

import "time"

const (
	RoutingKeyOrganizationCreated     = "organization.created"
	RoutingKeyOrganizationSuspended   = "organization.suspended"
	RoutingKeyOrganizationReactivated = "organization.reactivated"
	RoutingKeyOrganizationDeleted     = "organization.deleted"
	RoutingKeyMemberInvited           = "organization.member.invited"
	RoutingKeyMemberRemoved           = "organization.member.removed"
	RoutingKeyMemberRoleChanged       = "organization.member.role-changed"
	RoutingKeyMemberSuspended         = "organization.member.suspended"
	RoutingKeyMemberReinstated        = "organization.member.reinstated"
	RoutingKeyOwnershipTransferred    = "organization.ownership.transferred"
	RoutingKeyInvitationRequested     = "organization.invitation.requested"
	RoutingKeyInvitationDeclined      = "organization.invitation.declined"
	RoutingKeyWebhookHealthWarning    = "organization.webhook.health-warning"
	RoutingKeyWebhookAutoDisabled     = "organization.webhook.auto-disabled"
	RoutingKeyWebhookRetryRequested   = "organization.webhook.retry-requested"
)

// AddonSelection is one addon + quantity chosen at organization-creation
// time — carried on OrganizationCreated so billing can attach it to the new
// subscription before composing its first invoice, same "checkout cart"
// moment as Plan/Cycle/CouponCode below.
type AddonSelection struct {
	AddonID  string `json:"addon_id"`
	Quantity int    `json:"quantity"`
}

// OrganizationCreated is published after an organization is committed to the DB.
// Billing consumes this to provision a subscription for Plan (empty
// defaults to "solo") and Cycle (empty defaults to "monthly"), see
// billing.HandleOrganizationCreated; Notification consumes it to send a
// welcome message. Addons and CouponCode are optional and both already
// validated by the organization module before this event is published (see
// organization.createOrganization) — billing attaches them as-is.
type OrganizationCreated struct {
	OrganizationID string           `json:"organization_id"`
	Slug           string           `json:"slug"`
	Name           string           `json:"name"`
	CreatedBy      string           `json:"created_by"` // auth_sub of the owner
	CountryCode    string           `json:"country_code"`
	Plan           string           `json:"plan,omitempty"`
	Cycle          string           `json:"cycle,omitempty"`
	Addons         []AddonSelection `json:"addons,omitempty"`
	CouponCode     string           `json:"coupon_code,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
}

// OrganizationSuspended is published on suspension (billing failure, ToS).
// Other modules use this to gate access without a synchronous status check.
type OrganizationSuspended struct {
	OrganizationID string    `json:"organization_id"`
	Reason         string    `json:"reason"`
	SuspendedAt    time.Time `json:"suspended_at"`
}

// MemberInvited is published when a member is invited to an organization.
// Notification consumes this to send the invite email.
type MemberInvited struct {
	OrganizationID string `json:"organization_id"`
	Email          string `json:"email"`
	Role           string `json:"role"`
	InvitedBy      string `json:"invited_by"`
	Token          string `json:"token"`
}

// OrganizationReactivated is published when a suspended organization is unsuspended.
type OrganizationReactivated struct {
	OrganizationID string    `json:"organization_id"`
	ReactivatedAt  time.Time `json:"reactivated_at"`
}

// OrganizationDeleted signals other modules to clean up organization-scoped data.
type OrganizationDeleted struct {
	OrganizationID string    `json:"organization_id"`
	DeletedAt      time.Time `json:"deleted_at"`
}

// MemberRemoved is published when a member is removed from an organization.
type MemberRemoved struct {
	OrganizationID string `json:"organization_id"`
	AuthSub        string `json:"auth_sub"`
}

// MemberSuspended is published when a member's access to an organization is
// suspended — the membership row survives, but the member resolves to no
// role until reinstated. Notification tells the affected member in-app.
type MemberSuspended struct {
	OrganizationID string `json:"organization_id"`
	AuthSub        string `json:"auth_sub"`
}

// MemberReinstated is published when a suspended member's access is restored.
type MemberReinstated struct {
	OrganizationID string `json:"organization_id"`
	AuthSub        string `json:"auth_sub"`
}

// MemberRoleChanged is published when a member's role is updated.
type MemberRoleChanged struct {
	OrganizationID string `json:"organization_id"`
	AuthSub        string `json:"auth_sub"`
	Role           string `json:"role"`
}

// OwnershipTransferred is published when organization ownership moves to a new member.
type OwnershipTransferred struct {
	OrganizationID string `json:"organization_id"`
	PreviousOwner  string `json:"previous_owner"`
	NewOwner       string `json:"new_owner"`
}

// InvitationRequested is published when an invitee with an expired or lost
// invitation asks for a new one via the "Request new invite" action.
// Notification emails the ORIGINAL inviter (InvitedBy), not the requester —
// the requester has no permission to re-invite themselves.
type InvitationRequested struct {
	OrganizationID string `json:"organization_id"`
	InvitedBy      string `json:"invited_by"` // auth_sub to notify
	InviteeEmail   string `json:"invitee_email"`
}

// InvitationDeclined is published when an invitee declines a pending
// invitation. Notification tells the original inviter (InvitedBy) in-app
// only — a decline only affects the inviter's own todo list, not urgent
// enough to also warrant an email interruption.
type InvitationDeclined struct {
	OrganizationID string `json:"organization_id"`
	InvitedBy      string `json:"invited_by"` // auth_sub to notify
	InviteeEmail   string `json:"invitee_email"`
}

// WebhookHealthWarning is published the first time an endpoint's 24h
// success rate drops below 70% — health_warned_at prevents this from
// refiring on every subsequent failure once already below the threshold.
type WebhookHealthWarning struct {
	OrganizationID string `json:"organization_id"`
	EndpointID     string `json:"endpoint_id"`
	URL            string `json:"url"`
	SuccessPercent int    `json:"success_percent"`
}

// WebhookAutoDisabled is published when an endpoint is automatically
// disabled after 3 days of 100% delivery failure — re-enabling requires a
// passing test event, see selfUnsuspend-style guard in service.go.
type WebhookAutoDisabled struct {
	OrganizationID string `json:"organization_id"`
	EndpointID     string `json:"endpoint_id"`
	URL            string `json:"url"`
}

// WebhookRetryRequested is published once per failed delivery by the "retry
// all failed" bulk action, one message per delivery, so the actual HTTP
// attempts happen on the worker's consumer goroutine (rate-limited by
// PrefetchCount) instead of serially on the request goroutine.
type WebhookRetryRequested struct {
	OrganizationID string `json:"organization_id"`
	EndpointID     string `json:"endpoint_id"`
	DeliveryID     string `json:"delivery_id"`
}
