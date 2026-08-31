package organization_test

// Integration tests proving the three security-sensitive org mutations that
// only ever carried a bare HTTP-method audit label now record an explicit
// semantic audit.events.action:
//
//   webhook.secret_rotated   POST .../webhooks/:webhookID/rotate-secret
//   invite_code.regenerated  POST .../invite-code
//   member.removed           DELETE .../members/:authSub
//
// The action is only observable after the audit middleware runs, so these go
// through the real middleware + DB rather than asserting handler internals.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/audit"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// auditEngine mirrors organization.NewModuleEngine but mounts the real
// audit.Writer middleware on the /api group (in production it lives on the
// /api/v1 group, api_router.go). The caller owns the returned Writer and
// must Stop() it to force a synchronous flush before querying audit.events.
func auditEngine(pool *pgxpool.Pool, authSub string, w *audit.Writer) *gin.Engine {
	gin.SetMode(gin.TestMode)
	mod := organization.New(pool, messaging.NoopPublisher{}, "test-webhook-secret-encryption-key-0000", "", 1)
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub})
		c.Next()
	}
	noopGate := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	api.Use(w.Middleware())
	mod.Register(api, httpserver.RouteDeps{Auth: authMW, RateLimit: noopGate, Org: middleware.NewOrganizationMiddleware(mod), MFA: noopGate})
	return e
}

func TestIntegration_SemanticAuditActions(t *testing.T) {
	pool := testPool(t)

	// One org, reused by every subtest; the caller (testAuthSub) is its owner.
	writer := audit.NewWriter(pool, slog.Default(), 90)
	stopWriter := sync.OnceFunc(writer.Stop) // Stop closes a channel — must run exactly once
	engine := auditEngine(pool, testAuthSub, writer)

	serve := func(req *http.Request) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	w := serve(httpserver.JSONTestRequest(http.MethodPost, "/api/organizations",
		`{"slug":"integ-ws-audit-actions","name":"Audit Actions WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup org: want 201, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID := resp["data"].(map[string]any)["id"].(string)

	t.Cleanup(func() {
		stopWriter()
		pool.Exec(context.Background(), `DELETE FROM audit.events WHERE organization_id = $1`, orgID)
		pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
	})

	// --- exercise each endpoint once ---

	wCreate := serve(httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/webhooks",
		`{"url":"https://example.com/hook"}`))
	if wCreate.Code != http.StatusCreated {
		t.Fatalf("create webhook: want 201, got %d: %s", wCreate.Code, wCreate.Body)
	}
	var whResp map[string]any
	json.NewDecoder(wCreate.Body).Decode(&whResp)
	endpointID := whResp["data"].(map[string]any)["id"].(string)

	if got := serve(httpserver.JSONTestRequest(http.MethodPost,
		"/api/organizations/"+orgID+"/webhooks/"+endpointID+"/rotate-secret", "")); got.Code != http.StatusOK {
		t.Fatalf("rotate secret: want 200, got %d: %s", got.Code, got.Body)
	}

	if got := serve(httpserver.JSONTestRequest(http.MethodPost,
		"/api/organizations/"+orgID+"/invite-code", "")); got.Code != http.StatusOK {
		t.Fatalf("regenerate invite code: want 200, got %d: %s", got.Code, got.Body)
	}

	if got := serve(httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/members",
		`{"auth_sub":"integ_sub_audit_victim","role":"member"}`)); got.Code != http.StatusCreated {
		t.Fatalf("add member: want 201, got %d: %s", got.Code, got.Body)
	}
	if got := serve(httpserver.JSONTestRequest(http.MethodDelete,
		"/api/organizations/"+orgID+"/members/integ_sub_audit_victim", "")); got.Code != http.StatusNoContent {
		t.Fatalf("remove member: want 204, got %d: %s", got.Code, got.Body)
	}

	// Force the buffered writer to flush every queued event synchronously.
	stopWriter()

	// --- assert each route template recorded its semantic action ---

	cases := []struct {
		name     string
		resource string
		want     string
	}{
		{"rotate secret", "/api/organizations/:organizationID/webhooks/:webhookID/rotate-secret", "webhook.secret_rotated"},
		{"regenerate invite code", "/api/organizations/:organizationID/invite-code", "invite_code.regenerated"},
		{"remove member", "/api/organizations/:organizationID/members/:authSub", "member.removed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var action string
			err := pool.QueryRow(t.Context(), `
				SELECT action FROM audit.events
				WHERE organization_id = $1 AND resource = $2
				ORDER BY created_at DESC LIMIT 1`, orgID, tc.resource).Scan(&action)
			if err != nil {
				t.Fatalf("no audit row for %s: %v", tc.resource, err)
			}
			if action != tc.want {
				t.Errorf("audit action for %s: got %q, want %q", tc.resource, action, tc.want)
			}
		})
	}
}
