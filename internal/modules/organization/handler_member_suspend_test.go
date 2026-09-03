package organization_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/aasumitro/stratum/internal/platform/audit"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// TestIntegration_SuspendMember_RecordsSemanticAuditAction drives the real
// audit middleware + DB: a successful suspend must land an audit.events row
// labelled "member.suspended" rather than a bare POST, matching the
// member.removed precedent.
func TestIntegration_SuspendMember_RecordsSemanticAuditAction(t *testing.T) {
	pool := testPool(t)

	writer := audit.NewWriter(pool, slog.Default(), 90)
	stopWriter := sync.OnceFunc(writer.Stop)
	engine := auditEngine(pool, testAuthSub, writer)

	serve := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httpserver.JSONTestRequest(method, path, body))
		return w
	}

	w := serve(http.MethodPost, "/api/organizations",
		`{"slug":"integ-ws-suspend-audit","name":"Suspend Audit WS","plan":"solo","cycle":"monthly"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("setup org: want 201, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID := resp["data"].(map[string]any)["id"].(string)

	t.Cleanup(func() {
		stopWriter()
		pool.Exec(context.Background(), `DELETE FROM audit.events WHERE organization_id = $1`, orgID)
		pool.Exec(context.Background(), `DELETE FROM messaging.outbox WHERE payload->>'org_id' = $1`, orgID)
		pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
	})

	if got := serve(http.MethodPost, "/api/organizations/"+orgID+"/members",
		`{"auth_sub":"integ_sub_suspend_victim","role":"member"}`); got.Code != http.StatusCreated {
		t.Fatalf("add member: want 201, got %d: %s", got.Code, got.Body)
	}
	if got := serve(http.MethodPost,
		"/api/organizations/"+orgID+"/members/integ_sub_suspend_victim/suspend", ""); got.Code != http.StatusNoContent {
		t.Fatalf("suspend member: want 204, got %d: %s", got.Code, got.Body)
	}

	stopWriter() // force a synchronous flush before reading audit.events

	var action string
	err := pool.QueryRow(t.Context(), `
		SELECT action FROM audit.events
		WHERE organization_id = $1 AND resource = $2
		ORDER BY created_at DESC LIMIT 1`,
		orgID, "/api/organizations/:organizationID/members/:authSub/suspend").Scan(&action)
	if err != nil {
		t.Fatalf("no audit row for the suspend route: %v", err)
	}
	if action != "member.suspended" {
		t.Errorf("audit action: got %q, want %q", action, "member.suspended")
	}
}
