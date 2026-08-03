package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/aasumitro/stratum/internal/contracts"
)

const (
	orgCacheTTL        = 30 * time.Second
	memberRoleCacheTTL = 10 * time.Second
)

// CachedOrganizationReader wraps a contracts.OrganizationReader with Redis
// caching. Organization info is cached for 30s; member roles for 10s.
// Mutations (add/remove member, update organization) should call Invalidate.
type CachedOrganizationReader struct {
	inner contracts.OrganizationReader
	ns    *Namespace
}

// NewCachedOrganizationReader returns an OrganizationReader that caches in Redis.
func NewCachedOrganizationReader(
	inner contracts.OrganizationReader, client *redis.Client,
) *CachedOrganizationReader {
	return &CachedOrganizationReader{
		inner: inner,
		ns:    NewNamespace(client, "organization"),
	}
}

func (c *CachedOrganizationReader) GetOrganizationByID(
	ctx context.Context, organizationID string,
) (*contracts.OrganizationInfo, error) {
	key := fmt.Sprintf("org:%s", organizationID)

	if data, err := c.ns.Get(ctx, key); err == nil {
		ws := new(contracts.OrganizationInfo)
		if json.Unmarshal([]byte(data), ws) == nil {
			return ws, nil
		}
	}

	ws, err := c.inner.GetOrganizationByID(ctx, organizationID)
	if err != nil {
		return nil, fmt.Errorf("cache.GetOrganizationByID: %w", err)
	}

	if b, err := json.Marshal(ws); err == nil {
		_ = c.ns.Set(ctx, key, string(b), orgCacheTTL)
	}
	return ws, nil
}

func (c *CachedOrganizationReader) IsMember(
	ctx context.Context, organizationID, authSub string,
) (bool, error) {
	role, err := c.GetMemberRole(ctx, organizationID, authSub)
	if err != nil {
		return false, nil
	}
	return role != "", nil
}

func (c *CachedOrganizationReader) GetMemberRole(
	ctx context.Context, organizationID, authSub string,
) (string, error) {
	key := fmt.Sprintf("role:%s:%s", organizationID, authSub)

	if role, err := c.ns.Get(ctx, key); err == nil {
		return role, nil
	}

	role, err := c.inner.GetMemberRole(ctx, organizationID, authSub)
	if err != nil {
		return "", fmt.Errorf("cache.GetMemberRole: %w", err)
	}

	_ = c.ns.Set(ctx, key, role, memberRoleCacheTTL)
	return role, nil
}

// ListMemberAuthSubs passes through to the underlying reader — not cached (bulk list, no natural key).
func (c *CachedOrganizationReader) ListMemberAuthSubs(
	ctx context.Context, organizationID string,
) ([]string, error) {
	return c.inner.ListMemberAuthSubs(ctx, organizationID)
}

// GetFirstOrganizationIDForMember passes through to the underlying reader —
// not cached (rarely called, only on account email changes).
func (c *CachedOrganizationReader) GetFirstOrganizationIDForMember(
	ctx context.Context, authSub string,
) (string, error) {
	return c.inner.GetFirstOrganizationIDForMember(ctx, authSub)
}

// ListMembershipsForExport passes through to the underlying reader — not
// cached (a one-off async GDPR data-export call, not a hot path).
func (c *CachedOrganizationReader) ListMembershipsForExport(
	ctx context.Context, authSub string,
) ([]contracts.OrgMembershipInfo, error) {
	return c.inner.ListMembershipsForExport(ctx, authSub)
}

// ListOwnedOrganizationIDs passes through to the underlying reader — not
// cached (called once per organization-created event, not a hot path).
func (c *CachedOrganizationReader) ListOwnedOrganizationIDs(
	ctx context.Context, authSub string,
) ([]string, error) {
	return c.inner.ListOwnedOrganizationIDs(ctx, authSub)
}

// CountActiveOwnedOrganizations passes through to the underlying reader —
// not cached (called once per delete-account request, not a hot path).
func (c *CachedOrganizationReader) CountActiveOwnedOrganizations(
	ctx context.Context, authSub string,
) (int, error) {
	return c.inner.CountActiveOwnedOrganizations(ctx, authSub)
}

// InvalidateOrganization clears the cached organization info.
func (c *CachedOrganizationReader) InvalidateOrganization(
	ctx context.Context, organizationID string,
) {
	_ = c.ns.Delete(ctx, fmt.Sprintf("org:%s", organizationID))
}

// InvalidateMemberRole clears the cached role for a specific member.
func (c *CachedOrganizationReader) InvalidateMemberRole(
	ctx context.Context, organizationID, authSub string,
) {
	_ = c.ns.Delete(ctx, fmt.Sprintf("role:%s:%s", organizationID, authSub))
}

// compile-time check
var _ contracts.OrganizationReader = (*CachedOrganizationReader)(nil)
