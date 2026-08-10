package organization_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/apperr"
)

// TestRemoveMember_LiveOwnerCheck_ClosesStaleCacheGap simulates a gap where
// the HTTP handler's own owner check reads a Redis-cached organization snapshot
// that can be stale, so it alone can't be trusted to reject removing the current owner.
// Calling the service function directly (bypassing that cached check entirely,
// as if the cache had said "not the owner") proves the service itself now refuses
// regardless of what any upstream cache believed.
func TestRemoveMember_LiveOwnerCheck_ClosesStaleCacheGap(t *testing.T) {
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE slug = 'test-remove-owner-live-check'`)
	})

	orgID := setupOrgWithMembers(t, pool, "test-remove-owner-live-check")
	mod := organization.NewModuleForTest(pool)

	err := mod.RemoveMemberForTest(t.Context(), orgID, "owner_sub")
	if err == nil {
		t.Fatal("expected an error removing the live owner, got nil")
	}
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "CANNOT_REMOVE_OWNER" {
		t.Fatalf("expected CANNOT_REMOVE_OWNER, got %v", err)
	}

	role, roleErr := mod.GetMemberRole(t.Context(), orgID, "owner_sub")
	if roleErr != nil {
		t.Fatalf("owner membership should still exist: %v", roleErr)
	}
	if role != "owner" {
		t.Fatalf("expected owner_sub to still be owner, got role %q", role)
	}
}
