package contracts

import (
	"context"
	"time"
)

// Role constants for organization memberships.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// OrganizationInfo is the minimal projection of organization data other
// modules may depend on. Never import internal/modules/organization directly.
type OrganizationInfo struct {
	ID                string
	Slug              string
	Name              string
	Status            string // "active" | "suspended" | "deleted"
	OwnerID           string // auth_sub of the organization creator
	InviteCode        *string
	InviteCodeEnabled bool
	Timezone          string
	Locale            string
	CountryCode       string
	AllowedIPs        []string // CIDR blocks; empty = allow all
}

// OrganizationReader is implemented by the organization module and consumed by
// any module that needs to resolve organization data without owning its schema.
type OrganizationReader interface {
	GetOrganizationByID(ctx context.Context, organizationID string) (*OrganizationInfo, error)
	IsMember(ctx context.Context, organizationID, authSub string) (bool, error)
	GetMemberRole(ctx context.Context, organizationID, authSub string) (string, error)
	ListMemberAuthSubs(ctx context.Context, organizationID string) ([]string, error)
	// GetFirstOrganizationIDForMember returns the auth_sub's earliest-joined
	// organization — used to anchor personal (non-org-scoped) notifications,
	// since notification.messages.organization_id is NOT NULL. Empty string
	// if the member belongs to no organization.
	GetFirstOrganizationIDForMember(ctx context.Context, authSub string) (string, error)

	// ListMembershipsForExport returns every organization a user is or was
	// a member of, including soft-deleted organizations — used by the GDPR
	// data-export flow, which intentionally doesn't filter by status
	// (unlike a "my organizations" list).
	ListMembershipsForExport(ctx context.Context, authSub string) ([]OrgMembershipInfo, error)

	// ListOwnedOrganizationIDs returns the IDs of every organization a user
	// owns, regardless of status — used by billing's trial-eligibility
	// check ("has this owner ever had a subscription before"), which counts
	// against all owned organizations, matching the cross-schema JOIN this
	// replaces (no status filter). Not the same query as an owned-organization
	// *count* for account deletion, which does filter out deleted organizations —
	// don't reuse this for that without checking the filtering matches.
	ListOwnedOrganizationIDs(ctx context.Context, authSub string) ([]string, error)

	// CountActiveOwnedOrganizations returns how many non-deleted
	// organizations a user owns — used to block GDPR self-deletion while
	// the caller still owns organizations. Unlike ListOwnedOrganizationIDs,
	// this excludes deleted organizations: a user who only ever owned
	// since-deleted organizations should still be able to delete their account.
	CountActiveOwnedOrganizations(ctx context.Context, authSub string) (int, error)
}

// OrgMembershipInfo is the minimal projection of a user's organization
// membership for the GDPR data-export flow.
type OrgMembershipInfo struct {
	ID       string    `json:"id"`
	Slug     string    `json:"slug"`
	Name     string    `json:"name"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

// OrganizationSuspender is called by the billing module to suspend/unsuspend
// organizations on subscription expiry or reactivation.
type OrganizationSuspender interface {
	SuspendOrganization(ctx context.Context, organizationID, reason string) error
	UnsuspendOrganization(ctx context.Context, organizationID string) error
}

// OrganizationCacheInvalidator is called by the organization module after mutations
// to clear stale cached data. Implemented by CachedOrganizationReader.
type OrganizationCacheInvalidator interface {
	InvalidateOrganization(ctx context.Context, organizationID string)
	InvalidateMemberRole(ctx context.Context, organizationID, authSub string)
}

// OrganizationWriter is implemented by the organization module and consumed
// by modules that need to mutate organization-owned data across a schema
// boundary — currently only the GDPR account-deletion flow.
type OrganizationWriter interface {
	// RemoveAllMemberships deletes every membership row for a user across
	// all organizations.
	RemoveAllMemberships(ctx context.Context, authSub string) error
}

type OrganizationCommander interface {
	// ResolveDowngradeOverage brings organizationID's member count to at most
	// memberLimit, in one organization-owned transaction.
	// preferredMemberAuthSubs are removed first (silently ignoring IDs that
	// don't belong to this org or are the owner); if that isn't enough,
	// additional non-owner members are removed deterministically (see
	// selection order below) until the target is met or nothing removable
	// remains. A limit of -1 means unlimited (no removal). Returns everything
	// actually removed, split by whether it was in the caller's preferred
	// list or auto-selected. dryRun=true computes the same result without
	// deleting anything, for the preview endpoint.
	ResolveDowngradeOverage(
		ctx context.Context, organizationID string,
		preferredMemberAuthSubs []string,
		memberLimit int, dryRun bool,
	) (result OverageResolution, err error)
}

type OverageResolution struct {
	RemovedMemberAuthSubs  []string `json:"removed_member_auth_subs"`
	AutoSelectedMemberSubs []string `json:"auto_selected_member_subs"` // subset of RemovedMemberAuthSubs
}
