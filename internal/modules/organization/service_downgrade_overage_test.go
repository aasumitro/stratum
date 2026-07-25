package organization_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

func TestResolveDowngradeOverage(t *testing.T) {
	pool := testPool(t)

	// Clean up after test
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE slug LIKE 'test-downgrade-%'`)
	})

	// Setup: create org and members
	orgID := setupOrgWithMembers(t, pool, "test-downgrade-1")
	mod := organization.NewModuleForTest(pool)

	t.Run("owner always excluded and foreign IDs silently ignored", func(t *testing.T) {
		res, err := mod.ResolveDowngradeOverage(t.Context(), orgID,
			[]string{"owner_sub", "foreign_sub", "mem_2"}, 1,
			nil, -1, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Expected: owner and foreign ignored. We want to reach limit=1.
		// Current members = 4 (owner, mem_1, mem_2, mem_3). Target = 1 (owner). We need to remove 3.
		// Preferred provided: mem_2.
		// Auto-selected will remove mem_3 and mem_1.

		// Check removed members
		if len(res.RemovedMemberAuthSubs) != 3 {
			t.Fatalf("expected 3 members removed, got %d: %v", len(res.RemovedMemberAuthSubs), res.RemovedMemberAuthSubs)
		}

		foundMem2 := false
		for _, s := range res.RemovedMemberAuthSubs {
			if s == "owner_sub" {
				t.Errorf("owner was removed")
			}
			if s == "foreign_sub" {
				t.Errorf("foreign_sub was removed")
			}
			if s == "mem_2" {
				foundMem2 = true
			}
		}
		if !foundMem2 {
			t.Errorf("expected mem_2 to be removed")
		}
	})

	t.Run("dryRun true matches dryRun false", func(t *testing.T) {
		orgID2 := setupOrgWithMembers(t, pool, "test-downgrade-2")

		// Run dry run
		resDry, err := mod.ResolveDowngradeOverage(t.Context(), orgID2,
			nil, 2, nil, -1, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Run actual
		resReal, err := mod.ResolveDowngradeOverage(t.Context(), orgID2,
			nil, 2, nil, -1, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(resDry.RemovedMemberAuthSubs) != len(resReal.RemovedMemberAuthSubs) {
			t.Fatalf("dryRun and real mismatch in length")
		}
		for i, v := range resDry.RemovedMemberAuthSubs {
			if v != resReal.RemovedMemberAuthSubs[i] {
				t.Errorf("mismatch at %d: dry %s, real %s", i, v, resReal.RemovedMemberAuthSubs[i])
			}
		}
	})

	t.Run("unlimited (-1) skips removal", func(t *testing.T) {
		orgID3 := setupOrgWithMembers(t, pool, "test-downgrade-3")
		res, err := mod.ResolveDowngradeOverage(t.Context(), orgID3,
			[]string{"mem_1"}, -1, nil, -1, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res.RemovedMemberAuthSubs) > 0 {
			t.Errorf("expected 0 members removed for limit -1, got %v", res.RemovedMemberAuthSubs)
		}
	})

	// Regression: this struct is marshaled straight into an HTTP response
	// (the downgrade endpoint and the preview dry-run both return it) — a
	// nil Go slice here becomes JSON null, not [], which crashed the
	// frontend's success screen the moment it tried to .map() over a
	// dimension nothing was removed from (files, in every scenario this
	// plan's own tests ever exercised, since none of them touch storage).
	t.Run("untouched dimensions return empty slices, not nil", func(t *testing.T) {
		orgID4 := setupOrgWithMembers(t, pool, "test-downgrade-4")
		res, err := mod.ResolveDowngradeOverage(t.Context(), orgID4,
			nil, -1, nil, -1, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.RemovedMemberAuthSubs == nil {
			t.Error("RemovedMemberAuthSubs is nil, want an empty (non-nil) slice")
		}
		if res.AutoSelectedMemberSubs == nil {
			t.Error("AutoSelectedMemberSubs is nil, want an empty (non-nil) slice")
		}
		if res.RemovedFileIDs == nil {
			t.Error("RemovedFileIDs is nil, want an empty (non-nil) slice")
		}
		if res.AutoSelectedFileIDs == nil {
			t.Error("AutoSelectedFileIDs is nil, want an empty (non-nil) slice")
		}
	})

	// Edge case: if there aren't enough removable
	// members/files to fully close the gap, the downgrade still proceeds,
	// leaving the org over-limit on that dimension." Never actually
	// exercised until now — setupOrgWithMembers gives exactly 3 removable
	// members; asking for a limit of 0 (need to remove all 4, including
	// the un-removable owner) is the case where even removing every
	// removable member still leaves the org over limit.
	t.Run("not enough removable members still succeeds, leaves org over limit", func(t *testing.T) {
		orgID5 := setupOrgWithMembers(t, pool, "test-downgrade-5")
		res, err := mod.ResolveDowngradeOverage(t.Context(), orgID5,
			nil, 0, nil, -1, false)
		if err != nil {
			t.Fatalf("expected success even when the gap can't be fully closed, got error: %v", err)
		}
		if len(res.RemovedMemberAuthSubs) != 3 {
			t.Fatalf("expected all 3 removable members removed, got %d: %v", len(res.RemovedMemberAuthSubs), res.RemovedMemberAuthSubs)
		}
		var remaining int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM organization.memberships WHERE organization_id=$1`, orgID5).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining != 1 {
			t.Errorf("want 1 member left (the un-removable owner, still over the limit of 0), got %d", remaining)
		}
	})
}

// TestResolveDowngradeOverage_PublishesMemberRemovedEvents guards against
// the gap flagged as needing confirmation during implementation:
// the bulk removal path must publish organization.member.removed once per
// removed member (matching removeMember's single-member path), not skip
// notification entirely just because the removal happened in bulk.
func TestResolveDowngradeOverage_PublishesMemberRemovedEvents(t *testing.T) {
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE slug = 'test-downgrade-events'`)
	})

	orgID := setupOrgWithMembers(t, pool, "test-downgrade-events")
	pub := &capturingPublisher{}
	mod := organization.New(pool, pub)

	res, err := mod.ResolveDowngradeOverage(t.Context(), orgID, nil, 1, nil, -1, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.RemovedMemberAuthSubs) != 3 {
		t.Fatalf("expected 3 members removed, got %d: %v", len(res.RemovedMemberAuthSubs), res.RemovedMemberAuthSubs)
	}

	var publishedSubs []string
	for _, evt := range pub.published {
		if evt.routingKey != events.RoutingKeyMemberRemoved {
			continue
		}
		env, err := events.Decode[events.MemberRemoved](evt.body)
		if err != nil {
			t.Fatalf("decode published event: %v", err)
		}
		publishedSubs = append(publishedSubs, env.AuthSub)
	}

	if len(publishedSubs) != len(res.RemovedMemberAuthSubs) {
		t.Fatalf("published %d member-removed events, want one per removed member (%d): published=%v removed=%v",
			len(publishedSubs), len(res.RemovedMemberAuthSubs), publishedSubs, res.RemovedMemberAuthSubs)
	}
	for _, removed := range res.RemovedMemberAuthSubs {
		if !slices.Contains(publishedSubs, removed) {
			t.Errorf("no member-removed event published for %s", removed)
		}
	}
}

// fakeCacheInvalidator records every InvalidateMemberRole call so a test can
// assert on it without a real Redis instance.
type fakeCacheInvalidator struct {
	invalidatedRoles []struct{ organizationID, authSub string }
}

func (f *fakeCacheInvalidator) InvalidateOrganization(_ context.Context, _ string) {}

func (f *fakeCacheInvalidator) InvalidateMemberRole(_ context.Context, organizationID, authSub string) {
	f.invalidatedRoles = append(f.invalidatedRoles, struct{ organizationID, authSub string }{organizationID, authSub})
}

// TestResolveDowngradeOverage_InvalidatesRemovedMembersRoleCache guards
// against a gap found live: removeMember's single-member path invalidates
// the removed member's cached RBAC role immediately
// (handler_member.go's h.invalidateRole), via the HTTP handler layer. The
// bulk downgrade path never goes through that handler at all — it's called
// service-to-service from billing — so without this, a bulk-removed member
// would keep cached access to the organization until the cache's own TTL
// expired on its own, instead of losing it the moment the removal happens.
func TestResolveDowngradeOverage_InvalidatesRemovedMembersRoleCache(t *testing.T) {
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE slug = 'test-downgrade-cache'`)
	})

	orgID := setupOrgWithMembers(t, pool, "test-downgrade-cache")
	inv := &fakeCacheInvalidator{}
	mod := organization.New(pool, messaging.NoopPublisher{})
	mod.SetCacheInvalidator(inv)

	res, err := mod.ResolveDowngradeOverage(t.Context(), orgID, nil, 1, nil, -1, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.RemovedMemberAuthSubs) != 3 {
		t.Fatalf("expected 3 members removed, got %d: %v", len(res.RemovedMemberAuthSubs), res.RemovedMemberAuthSubs)
	}

	var invalidatedSubs []string
	for _, r := range inv.invalidatedRoles {
		if r.organizationID != orgID {
			t.Errorf("invalidated role for wrong org: got %s, want %s", r.organizationID, orgID)
		}
		invalidatedSubs = append(invalidatedSubs, r.authSub)
	}
	if len(invalidatedSubs) != len(res.RemovedMemberAuthSubs) {
		t.Fatalf("invalidated %d roles, want one per removed member (%d): invalidated=%v removed=%v",
			len(invalidatedSubs), len(res.RemovedMemberAuthSubs), invalidatedSubs, res.RemovedMemberAuthSubs)
	}
	for _, removed := range res.RemovedMemberAuthSubs {
		if !slices.Contains(invalidatedSubs, removed) {
			t.Errorf("no cache invalidation for removed member %s", removed)
		}
	}
}

// setupOrgWithMembers creates an organization with 1 owner and 3 members.
func setupOrgWithMembers(t *testing.T, pool *pgxpool.Pool, slug string) string {
	t.Helper()
	var orgID string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO organization.organizations (slug, name, owner_id) 
		VALUES ($1, 'Test', 'owner_sub') RETURNING id`, slug).Scan(&orgID)
	if err != nil {
		t.Fatalf("insert org: %v", err)
	}

	// Owner
	pool.Exec(context.Background(), `
		INSERT INTO organization.memberships (organization_id, auth_sub, role, joined_at) 
		VALUES ($1, 'owner_sub', 'owner', now())`, orgID)

	// Members
	for i := 1; i <= 3; i++ {
		pool.Exec(context.Background(), `
			INSERT INTO organization.memberships (organization_id, auth_sub, role, joined_at) 
			VALUES ($1, $2, 'member', $3)`, orgID, fmt.Sprintf("mem_%d", i), time.Now().Add(time.Duration(i)*time.Hour))
	}
	return orgID
}
