package organization_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// suspendActiveMemberCount reads the live active-member count straight from
// the table — the same predicate countActiveMembers applies, checked
// independently of the repository code under test.
func suspendActiveMemberCount(t *testing.T, pool *pgxpool.Pool, orgID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM organization.memberships WHERE organization_id = $1 AND status = 'active'`,
		orgID).Scan(&n); err != nil {
		t.Fatalf("count active members: %v", err)
	}
	return n
}

// suspendOutboxCount counts outbox rows for a routing key scoped to one org —
// events.Enqueue writes these in the same transaction as the status change.
func suspendOutboxCount(t *testing.T, pool *pgxpool.Pool, routingKey, orgID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM messaging.outbox WHERE routing_key = $1 AND payload->>'org_id' = $2`,
		routingKey, orgID).Scan(&n); err != nil {
		t.Fatalf("count outbox rows: %v", err)
	}
	return n
}

func cleanupSuspendOrg(t *testing.T, pool *pgxpool.Pool, orgID string) {
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM messaging.outbox WHERE payload->>'org_id' = $1`, orgID)
		pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
	})
}

func serveOrg(t *testing.T, engine http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httpserver.JSONTestRequest(method, path, ""))
	return w
}

func TestIntegration_SuspendMember_BlocksAccessAndFreesSeat(t *testing.T) {
	pool := testPool(t)
	orgID := setupOrgWithMembers(t, pool, "test-suspend-access")
	cleanupSuspendOrg(t, pool, orgID)

	engine, mod := organization.NewModuleEngineAndModule(pool, "owner_sub")

	before := suspendActiveMemberCount(t, pool, orgID) // owner + 3 members
	if w := serveOrg(t, engine, http.MethodPost, "/api/organizations/"+orgID+"/members/mem_1/suspend"); w.Code != http.StatusNoContent {
		t.Fatalf("suspend: want 204, got %d: %s", w.Code, w.Body)
	}

	if after := suspendActiveMemberCount(t, pool, orgID); after != before-1 {
		t.Errorf("active member count: want %d, got %d", before-1, after)
	}

	if role, _ := mod.GetMemberRole(t.Context(), orgID, "mem_1"); role != "" {
		t.Errorf("suspended member should resolve to no role, got %q", role)
	}

	// A suspended member is bounced from every org-scoped route.
	memEngine, _ := organization.NewModuleEngineAndModule(pool, "mem_1")
	if w := serveOrg(t, memEngine, http.MethodGet, "/api/organizations/"+orgID+"/members"); w.Code != http.StatusForbidden {
		t.Errorf("suspended member on scoped route: want 403, got %d", w.Code)
	}

	if n := suspendOutboxCount(t, pool, events.RoutingKeyMemberSuspended, orgID); n != 1 {
		t.Errorf("outbox rows for %s: want 1, got %d", events.RoutingKeyMemberSuspended, n)
	}

	// The admin members list carries status for every row and still shows the
	// suspended member (unlike the access/seat queries, which filter it out).
	lw := serveOrg(t, engine, http.MethodGet, "/api/organizations/"+orgID+"/members")
	if lw.Code != http.StatusOK {
		t.Fatalf("list members: want 200, got %d: %s", lw.Code, lw.Body)
	}
	var listResp struct {
		Data []struct {
			AuthSub string `json:"auth_sub"`
			Status  string `json:"status"`
		} `json:"data"`
	}
	if err := json.NewDecoder(lw.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode members list: %v", err)
	}
	statuses := map[string]string{}
	for _, m := range listResp.Data {
		statuses[m.AuthSub] = m.Status
	}
	if statuses["mem_1"] != "suspended" {
		t.Errorf("mem_1 status in list: want \"suspended\", got %q", statuses["mem_1"])
	}
	if statuses["owner_sub"] != "active" {
		t.Errorf("owner status in list: want \"active\", got %q", statuses["owner_sub"])
	}

	// Reinstate restores the role and the seat.
	if w := serveOrg(t, engine, http.MethodPost, "/api/organizations/"+orgID+"/members/mem_1/reinstate"); w.Code != http.StatusNoContent {
		t.Fatalf("reinstate: want 204, got %d: %s", w.Code, w.Body)
	}
	if after := suspendActiveMemberCount(t, pool, orgID); after != before {
		t.Errorf("active member count after reinstate: want %d, got %d", before, after)
	}
	if role, err := mod.GetMemberRole(t.Context(), orgID, "mem_1"); err != nil || role != "member" {
		t.Errorf("reinstated member role: want \"member\", got %q (err %v)", role, err)
	}
	if n := suspendOutboxCount(t, pool, events.RoutingKeyMemberReinstated, orgID); n != 1 {
		t.Errorf("outbox rows for %s: want 1, got %d", events.RoutingKeyMemberReinstated, n)
	}
}

func TestIntegration_ReinstateMember_RechecksSeatLimit(t *testing.T) {
	pool := testPool(t)
	orgID := setupOrgWithMembers(t, pool, "test-reinstate-limit")
	cleanupSuspendOrg(t, pool, orgID)

	engine, mod := organization.NewModuleEngineAndModule(pool, "owner_sub")
	// Plan seat limit == the active count once one member is suspended, so
	// reinstating that member would push the org back over its cap.
	mod.SetBillingReader(stubMemberLimitReader{limit: 3})

	if w := serveOrg(t, engine, http.MethodPost, "/api/organizations/"+orgID+"/members/mem_1/suspend"); w.Code != http.StatusNoContent {
		t.Fatalf("suspend mem_1: want 204, got %d: %s", w.Code, w.Body)
	}

	w := serveOrg(t, engine, http.MethodPost, "/api/organizations/"+orgID+"/members/mem_1/reinstate")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("reinstate at cap: want 422, got %d: %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "PLAN_LIMIT_REACHED") {
		t.Errorf("reinstate at cap: body should carry PLAN_LIMIT_REACHED, got %s", w.Body)
	}
	// Still suspended — no partial state.
	if role, _ := mod.GetMemberRole(t.Context(), orgID, "mem_1"); role != "" {
		t.Errorf("member should stay suspended after a failed reinstate, got role %q", role)
	}

	// Free a seat, then the same reinstate succeeds.
	if w := serveOrg(t, engine, http.MethodPost, "/api/organizations/"+orgID+"/members/mem_2/suspend"); w.Code != http.StatusNoContent {
		t.Fatalf("suspend mem_2: want 204, got %d: %s", w.Code, w.Body)
	}
	if w := serveOrg(t, engine, http.MethodPost, "/api/organizations/"+orgID+"/members/mem_1/reinstate"); w.Code != http.StatusNoContent {
		t.Fatalf("reinstate after a seat freed: want 204, got %d: %s", w.Code, w.Body)
	}
	if role, err := mod.GetMemberRole(t.Context(), orgID, "mem_1"); err != nil || role != "member" {
		t.Errorf("reinstated member role: want \"member\", got %q (err %v)", role, err)
	}
}

func TestIntegration_SuspendReinstate_Guards(t *testing.T) {
	pool := testPool(t)
	orgID := setupOrgWithMembers(t, pool, "test-suspend-guards")
	cleanupSuspendOrg(t, pool, orgID)
	seedOrgMember(t, pool, orgID, "admin_sub", "admin")

	ownerEng, _ := organization.NewModuleEngineAndModule(pool, "owner_sub")
	adminEng, _ := organization.NewModuleEngineAndModule(pool, "admin_sub")
	memberEng, _ := organization.NewModuleEngineAndModule(pool, "mem_2")

	base := "/api/organizations/" + orgID + "/members/"

	if w := serveOrg(t, adminEng, http.MethodPost, base+"owner_sub/suspend"); w.Code != http.StatusForbidden {
		t.Errorf("suspend owner: want 403, got %d", w.Code)
	}
	if w := serveOrg(t, ownerEng, http.MethodPost, base+"owner_sub/suspend"); w.Code != http.StatusBadRequest {
		t.Errorf("suspend self: want 400, got %d", w.Code)
	}
	if w := serveOrg(t, memberEng, http.MethodPost, base+"mem_1/suspend"); w.Code != http.StatusForbidden {
		t.Errorf("suspend as a plain member: want 403 (adminUp), got %d", w.Code)
	}
	if w := serveOrg(t, ownerEng, http.MethodPost, base+"ghost_sub/suspend"); w.Code != http.StatusNotFound {
		t.Errorf("suspend a non-member: want 404, got %d", w.Code)
	}
	if w := serveOrg(t, ownerEng, http.MethodPost, base+"mem_3/reinstate"); w.Code != http.StatusConflict {
		t.Errorf("reinstate an active member: want 409, got %d", w.Code)
	}

	// Double-suspend is a distinct 409.
	if w := serveOrg(t, ownerEng, http.MethodPost, base+"mem_1/suspend"); w.Code != http.StatusNoContent {
		t.Fatalf("first suspend: want 204, got %d: %s", w.Code, w.Body)
	}
	if w := serveOrg(t, ownerEng, http.MethodPost, base+"mem_1/suspend"); w.Code != http.StatusConflict {
		t.Errorf("second suspend: want 409, got %d", w.Code)
	}
}
