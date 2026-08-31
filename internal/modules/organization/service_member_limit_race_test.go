package organization_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// stubMemberLimitReader is a contracts.BillingReader whose CheckUsageLimit
// always returns a fixed limit and a stale current that these tests never
// update — that staleness is deliberate: it's what checkMemberLimitLocked
// must ignore in favor of a live count for the race it closes to actually
// be closed.
type stubMemberLimitReader struct{ limit int }

func (stubMemberLimitReader) GetSubscriptionBySubject(context.Context, string, string) (*contracts.SubscriptionInfo, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s stubMemberLimitReader) CheckUsageLimit(context.Context, string, string) (current int64, limit int, err error) {
	return 0, s.limit, nil
}

func (stubMemberLimitReader) CheckFeatureAccess(context.Context, string, string) error { return nil }

// insertRaceTestOrg creates a bare organization with just its owner
// membership — the live member count starts at exactly 1, the tight
// starting point a concurrent-add race needs to actually hit a limit of 2.
func insertRaceTestOrg(t *testing.T, pool *pgxpool.Pool, slug, ownerSub string) string {
	t.Helper()
	var orgID string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO organization.organizations (slug, name, owner_id, invite_code, invite_code_enabled)
		VALUES ($1, 'Race Test', $2, $3, true) RETURNING id`,
		slug, ownerSub, slug+"-code").Scan(&orgID)
	if err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO organization.memberships (organization_id, auth_sub, role, joined_at)
		VALUES ($1, $2, 'owner', now())`, orgID, ownerSub); err != nil {
		t.Fatalf("insert owner membership: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
	})
	return orgID
}

func assertExactlyOneSuccess(t *testing.T, statuses []int, wantSuccess int) {
	t.Helper()
	successCount := 0
	for _, code := range statuses {
		if code == wantSuccess {
			successCount++
		}
	}
	if successCount != 1 {
		t.Errorf("want exactly 1 of %d concurrent calls to succeed, got %d (statuses: %v)", len(statuses), successCount, statuses)
	}
}

func assertMembershipCount(t *testing.T, pool *pgxpool.Pool, orgID string, want int) {
	t.Helper()
	var got int
	pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM organization.memberships WHERE organization_id = $1`, orgID).Scan(&got)
	if got != want {
		t.Errorf("want %d membership rows after the race, got %d", want, got)
	}
}

// TestIntegration_AddMember_ConcurrentAdds_LimitEnforced races two
// concurrent addMember calls (adding two different auth subs) against an
// organization at exactly one seat under its plan limit. Before the fix,
// both calls would read the same stale billing.usage-cached count, both
// see "under limit", and both succeed — overshooting the seat limit.
func TestIntegration_AddMember_ConcurrentAdds_LimitEnforced(t *testing.T) {
	pool := testPool(t)
	orgID := insertRaceTestOrg(t, pool, "race-add-member", "race_add_owner")

	engine, mod := organization.NewModuleEngineWithPublisher(pool, "race_add_owner")
	mod.SetBillingReader(stubMemberLimitReader{limit: 2})

	targets := []string{"race_add_member_1", "race_add_member_2"}
	results := make([]int, len(targets))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, sub := range targets {
		wg.Add(1)
		go func(i int, sub string) {
			defer wg.Done()
			<-start
			body := fmt.Sprintf(`{"auth_sub":%q,"role":"member"}`, sub)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/members", body))
			results[i] = w.Code
		}(i, sub)
	}
	close(start)
	wg.Wait()

	assertExactlyOneSuccess(t, results, http.StatusCreated)
	assertMembershipCount(t, pool, orgID, 2) // owner + exactly one winner
}

// TestIntegration_JoinByCode_ConcurrentJoins_LimitEnforced races two
// different users joining the same organization via the same invite code at
// the same time, against an organization at exactly one seat under its plan
// limit.
func TestIntegration_JoinByCode_ConcurrentJoins_LimitEnforced(t *testing.T) {
	pool := testPool(t)
	orgID := insertRaceTestOrg(t, pool, "race-join-code", "race_join_owner")

	joiners := []string{"race_joiner_1", "race_joiner_2"}
	engines := make([]http.Handler, len(joiners))
	for i, sub := range joiners {
		e, mod := organization.NewModuleEngineWithPublisher(pool, sub)
		mod.SetBillingReader(stubMemberLimitReader{limit: 2})
		engines[i] = e
	}

	results := make([]int, len(joiners))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range joiners {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			body := `{"code":"race-join-code-code"}`
			w := httptest.NewRecorder()
			engines[i].ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/join", body))
			results[i] = w.Code
		}(i)
	}
	close(start)
	wg.Wait()

	assertExactlyOneSuccess(t, results, http.StatusOK)
	assertMembershipCount(t, pool, orgID, 2) // owner + exactly one winner
}

// TestIntegration_AcceptInvitation_ConcurrentAccepts_LimitEnforced races two
// different invitees accepting two separate pending invitations to the same
// organization at the same time, against an organization at exactly one
// seat under its plan limit.
func TestIntegration_AcceptInvitation_ConcurrentAccepts_LimitEnforced(t *testing.T) {
	pool := testPool(t)
	orgID := insertRaceTestOrg(t, pool, "race-accept-invite", "race_accept_owner")

	type invitee struct {
		authSub, email, token string
	}
	invitees := []invitee{
		{"race_invitee_1", "invitee1@race.test", "race-accept-token-1"},
		{"race_invitee_2", "invitee2@race.test", "race-accept-token-2"},
	}
	for _, inv := range invitees {
		_, err := pool.Exec(context.Background(), `
			INSERT INTO organization.invitations (organization_id, email, role, token, invited_by, expires_at)
			VALUES ($1, $2, 'member', $3, 'race_accept_owner', now() + interval '7 days')`,
			orgID, inv.email, inv.token)
		if err != nil {
			t.Fatalf("insert invitation for %s: %v", inv.email, err)
		}
	}

	results := make([]int, len(invitees))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, inv := range invitees {
		wg.Add(1)
		go func(i int, inv invitee) {
			defer wg.Done()
			e := organization.NewModuleEngineWithBillingReaderAndEmail(pool, inv.authSub, inv.email, stubMemberLimitReader{limit: 2})
			<-start
			body := fmt.Sprintf(`{"token":%q}`, inv.token)
			w := httptest.NewRecorder()
			e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/api/invitations/accept", body))
			results[i] = w.Code
		}(i, inv)
	}
	close(start)
	wg.Wait()

	assertExactlyOneSuccess(t, results, http.StatusNoContent)
	assertMembershipCount(t, pool, orgID, 2) // owner + exactly one winner
}
