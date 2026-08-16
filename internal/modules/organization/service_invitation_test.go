package organization_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

func TestIntegration_RevokeInvitation_CrossTenant_404(t *testing.T) {
	pool := testPool(t)

	// 1. Create Org A and Org B directly in the DB
	orgA := insertRaceTestOrg(t, pool, "cross-tenant-a", "attacker_sub")
	orgB := insertRaceTestOrg(t, pool, "cross-tenant-b", "victim_sub")

	// 2. Insert a pending invitation into Org B
	var invitationID string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO organization.invitations (organization_id, email, role, token, invited_by, expires_at)
		VALUES ($1, $2, 'member', $3, 'victim_sub', now() + interval '7 days')
		RETURNING id`,
		orgB, "target@victim.test", "cross-tenant-token").Scan(&invitationID)
	if err != nil {
		t.Fatalf("insert org B invitation: %v", err)
	}

	// 3. Use the organization module's engine to send a DELETE request as org A's admin
	engine := organization.NewModuleEngine(pool, "attacker_sub")

	w := httptest.NewRecorder()
	req := httpserver.JSONTestRequest(http.MethodDelete, "/api/organizations/"+orgA+"/invitations/"+invitationID, "")
	engine.ServeHTTP(w, req)

	// 4. Assert 404 response
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404 Not Found for cross-tenant invitation revoke, got %d: %s", w.Code, w.Body)
	}

	// 5. Assert org B's invitation row still exists via a direct SQL query
	var exists bool
	err = pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM organization.invitations WHERE id = $1)`, invitationID).Scan(&exists)
	if err != nil {
		t.Fatalf("query invitation existence: %v", err)
	}
	if !exists {
		t.Errorf("expected org B's invitation to still exist, but it was deleted")
	}
}
