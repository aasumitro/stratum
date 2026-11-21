package billing_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
)

// The organization module resolves a downgrade's member overage in a
// transaction of its own, from the same pool billing uses. A route that held a
// request-long transaction while calling it would wait for a second connection
// it never gets once the pool is busy. With one connection that is every
// request, so these must complete on a pool of one.
func TestOverage_PreviewAndDowngrade_CompleteOnAPoolOfOneConnection(t *testing.T) {
	admin := testPoolBilling(t)
	cfg, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	const (
		owner  = "integ_overage_pool_owner"
		member = "integ_overage_pool_member"
		orgID  = "00000000-0000-0000-0000-00000000d0c1"
	)
	cleanup := func() {
		ctx := context.Background()
		cleanupBillingByOrganization(admin, orgID)
		admin.Exec(ctx, `DELETE FROM organization.memberships WHERE organization_id = $1`, orgID)
		admin.Exec(ctx, `DELETE FROM organization.organizations WHERE id = $1`, orgID)
	}
	cleanup()
	t.Cleanup(cleanup)
	seedBillingOrganization(admin, orgID, owner)
	if _, err := admin.Exec(t.Context(), `
		INSERT INTO organization.memberships (organization_id, auth_sub, role, joined_at)
		VALUES ($1, $2, 'owner', now()), ($1, $3, 'member', now())`, orgID, owner, member); err != nil {
		t.Fatalf("seed members: %v", err)
	}

	orgMod := organization.New(pool, testWebhookEncryptionKey, "", 1)
	mod := billing.NewModuleForTest(pool, stubRefReader{})
	mod.SetOrganizationReader(orgMod)
	mod.SetOrganizationCommander(orgMod)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, owner)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if _, err := admin.Exec(t.Context(), `
		UPDATE billing.subscriptions SET plan = 'growth', cycle = 'monthly', status = 'trialing'
		WHERE subject_type = 'organization' AND subject_id = $1`, orgID); err != nil {
		t.Fatalf("set plan: %v", err)
	}
	if err := mod.RecordUsage(t.Context(), orgID, "members", 2); err != nil {
		t.Fatalf("record usage: %v", err)
	}

	gin.SetMode(gin.TestMode)
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: owner})
		c.Next()
	}
	orgMW := func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{
			ID: orgID, Slug: "test-ws", Name: "Test WS", Status: "active", OwnerID: owner,
		})
		c.Set("organization.role", contracts.RoleOwner)
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	mod.Register(e.Group("/api"), httpserver.RouteDeps{
		Auth: authMW, RateLimit: noopMW, Org: orgMW, Idempotency: noopMW,
		RLS: middleware.NewRLSTxMiddleware(pool), MFA: noopMW,
	})

	serve := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httpserver.JSONTestRequest(method, path, body).WithContext(ctx))
		return w
	}

	w := serve(http.MethodGet, billingURL(orgID)+"/preview?plan=solo&cycle=monthly", "")
	if w.Code != http.StatusOK {
		t.Fatalf("preview: want 200, got %d: %s", w.Code, w.Body)
	}
	var preview struct {
		Data struct {
			Overage *struct {
				Members struct {
					AutoSelectRemovals []string `json:"auto_select_removals"`
				} `json:"members"`
			} `json:"overage"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if preview.Data.Overage == nil || len(preview.Data.Overage.Members.AutoSelectRemovals) != 1 {
		t.Fatalf("preview: want an overage naming one member to remove, got %+v", preview.Data.Overage)
	}

	w = serve(http.MethodPost, billingURL(orgID)+"/downgrade", `{"plan":"solo","cycle":"monthly"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("downgrade: want 200, got %d: %s", w.Code, w.Body)
	}
	var plan string
	admin.QueryRow(t.Context(), `SELECT plan FROM billing.subscriptions WHERE subject_id = $1`, orgID).Scan(&plan)
	var members int
	admin.QueryRow(t.Context(), `SELECT COUNT(*) FROM organization.memberships WHERE organization_id = $1`, orgID).Scan(&members)
	if plan != "solo" || members != 1 {
		t.Errorf("after the downgrade: want plan solo with one member, got %q with %d", plan, members)
	}
}
