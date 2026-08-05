package billing_test

// HTTP-level tests for the three "undo a scheduled amendment" routes added
// alongside downgrade/addon/cancellation scheduling: MFA enforcement (or its
// deliberate absence on cancel/undo), ownerOnly enforcement, and the
// "nothing to undo" error path returning a real HTTP status instead of a
// bare 500.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	jwtgo "github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
)

// stubMFAUserReader reports a fixed MFA-enabled status for every subject —
// enough to drive middleware.RequireMFAIfEnabled's two branches (enabled vs
// not) without a real account-module dependency.
type stubMFAUserReader struct{ enabled bool }

func (s stubMFAUserReader) GetUserByAuthSub(_ context.Context, sub string) (*contracts.UserInfo, error) {
	return &contracts.UserInfo{AuthSub: sub}, nil
}

func (s stubMFAUserReader) GetUserByEmail(_ context.Context, _ string) (*contracts.UserInfo, error) {
	return nil, errors.New("not found")
}

func (s stubMFAUserReader) GetUsersByAuthSubs(_ context.Context, _ []string) (map[string]contracts.UserInfo, error) {
	return nil, nil
}

func (s stubMFAUserReader) IsMFAEnabled(_ context.Context, _ string) (bool, error) {
	return s.enabled, nil
}

// newAmendmentMFAEngine wires a real DB-backed billing module with the real
// middleware.RequireMFAIfEnabled gate (every other test engine in this
// package wires MFA as a no-op) so the undo routes' MFA policy is exercised
// against actual middleware behavior, not just read off the route
// registration line. Every test using this engine needs the caller to have
// MFA enabled (that's the only state RequireMFAIfEnabled branches on); aal2
// controls whether their claims carry a verified second factor.
func newAmendmentMFAEngine(pool *pgxpool.Pool, authSub, orgID string, aal2 bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	mod := billing.NewModuleForTest(pool, nil)
	e := gin.New()
	authMW := func(c *gin.Context) {
		claims := middleware.Claims{Subject: authSub}
		if aal2 {
			claims.Raw = jwtgo.MapClaims{"aal": "aal2"}
		}
		c.Set("auth.claims", claims)
		c.Next()
	}
	orgMW := func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{
			ID: orgID, Slug: "test-ws", Name: "Test WS", Status: "active", OwnerID: authSub,
		})
		c.Set("organization.role", contracts.RoleOwner)
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	mod.Register(api, httpserver.RouteDeps{
		Auth: authMW, RateLimit: noopMW, Org: orgMW, Idempotency: noopMW, RLS: noopMW,
		MFA: middleware.RequireMFAIfEnabled(stubMFAUserReader{enabled: true}),
	})
	return e
}

// seedActiveOrgWithScheduledDowngrade provisions an organization, forces its
// subscription active, and schedules a plan downgrade directly via SQL —
// this package's amendment service tests already cover the write path
// itself (service_downgrade_amendment_test.go); these handler tests only
// need the resulting state to exist.
func seedActiveOrgWithScheduledDowngrade(t *testing.T, pool *pgxpool.Pool, orgID, authSub string) {
	t.Helper()
	setupBillingTest(t, pool, orgID)
	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, authSub)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET status = 'active', scheduled_plan = 'growth', scheduled_cycle = 'monthly', scheduled_requested_at = now() WHERE id = $1`,
		subID); err != nil {
		t.Fatalf("seed scheduled downgrade: %v", err)
	}
}

func seedActiveOrgNoSchedule(t *testing.T, pool *pgxpool.Pool, orgID, authSub string) string {
	t.Helper()
	setupBillingTest(t, pool, orgID)
	mod := billing.NewModuleForTest(pool, nil)
	if err := mod.Worker.HandleOrganizationCreated(t.Context(), encodeOrganizationCreatedEventFor(orgID, authSub)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subID := getSubscriptionID(pool, orgID)
	if _, err := pool.Exec(t.Context(), `UPDATE billing.subscriptions SET status = 'active' WHERE id = $1`, subID); err != nil {
		t.Fatalf("force active: %v", err)
	}
	return subID
}

func seedActiveOrgWithScheduledAddonRemoval(t *testing.T, pool *pgxpool.Pool, orgID, authSub string) {
	t.Helper()
	subID := seedActiveOrgNoSchedule(t, pool, orgID, authSub)
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO billing.subscription_addons (subscription_id, addon_id, quantity, scheduled_quantity, scheduled_requested_at)
		VALUES ($1, 'extra-seat', 5, 0, now())`, subID); err != nil {
		t.Fatalf("seed scheduled addon removal: %v", err)
	}
}

func seedActiveOrgWithScheduledCancellation(t *testing.T, pool *pgxpool.Pool, orgID, authSub string) {
	t.Helper()
	subID := seedActiveOrgNoSchedule(t, pool, orgID, authSub)
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET scheduled_cancel_at = now() WHERE id = $1`, subID); err != nil {
		t.Fatalf("seed scheduled cancellation: %v", err)
	}
}

// --- ownerOnly enforcement (matching this file's existing coverage shape) ---

func TestUndoDowngrade_NonOwnerForbidden(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngineWithCaller("sub_caller", "sub_owner").ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/billing/downgrade/undo", ""))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestUndoAddonQuantityChange_NonOwnerForbidden(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngineWithCaller("sub_caller", "sub_owner").ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/billing/addons/extra-seat/undo", ""))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestUndoCancellation_NonOwnerForbidden(t *testing.T) {
	w := httptest.NewRecorder()
	billing.NewHandlerEngineWithCaller("sub_caller", "sub_owner").ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/billing/cancel/undo", ""))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

// --- MFA enforcement ---

func TestIntegration_UndoDowngrade_RequiresMFA(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_undo_downgrade_mfa_user"
		orgID = "00000000-0000-0000-0000-000000000f30"
	)
	seedActiveOrgWithScheduledDowngrade(t, pool, orgID, user)

	e := newAmendmentMFAEngine(pool, user, orgID, false)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/downgrade/undo", ""))
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 without a fresh MFA assertion, got %d: %s", w.Code, w.Body)
	}
}

func TestIntegration_UndoDowngrade_MFAStepUpAllowed(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_undo_downgrade_mfa2_user"
		orgID = "00000000-0000-0000-0000-000000000f31"
	)
	seedActiveOrgWithScheduledDowngrade(t, pool, orgID, user)

	e := newAmendmentMFAEngine(pool, user, orgID, true)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/downgrade/undo", ""))
	if w.Code == http.StatusForbidden {
		t.Fatalf("a fresh MFA assertion must not be rejected, got 403: %s", w.Body)
	}
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d: %s", w.Code, w.Body)
	}
}

func TestIntegration_UndoAddonQuantityChange_RequiresMFA(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_undo_addon_mfa_user"
		orgID = "00000000-0000-0000-0000-000000000f32"
	)
	seedActiveOrgWithScheduledAddonRemoval(t, pool, orgID, user)

	e := newAmendmentMFAEngine(pool, user, orgID, false)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/addons/extra-seat/undo", ""))
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 without a fresh MFA assertion, got %d: %s", w.Code, w.Body)
	}
}

func TestIntegration_UndoAddonQuantityChange_MFAStepUpAllowed(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_undo_addon_mfa2_user"
		orgID = "00000000-0000-0000-0000-000000000f33"
	)
	seedActiveOrgWithScheduledAddonRemoval(t, pool, orgID, user)

	e := newAmendmentMFAEngine(pool, user, orgID, true)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/addons/extra-seat/undo", ""))
	if w.Code == http.StatusForbidden {
		t.Fatalf("a fresh MFA assertion must not be rejected, got 403: %s", w.Body)
	}
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d: %s", w.Code, w.Body)
	}
}

// TestIntegration_UndoCancellation_NoMFARequired guards a security-relevant
// route-registration detail: cancel/undo must not carry deps.MFA the way
// downgrade/addon undo do. An MFA-enabled caller with no aal2 assertion must
// still succeed here, not be rejected the way the same caller would be on
// the other two undo routes above.
func TestIntegration_UndoCancellation_NoMFARequired(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_undo_cancel_nomfa_user"
		orgID = "00000000-0000-0000-0000-000000000f34"
	)
	seedActiveOrgWithScheduledCancellation(t, pool, orgID, user)

	e := newAmendmentMFAEngine(pool, user, orgID, false)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel/undo", ""))
	if w.Code == http.StatusForbidden {
		t.Fatalf("cancel/undo must not require MFA, got 403: %s", w.Body)
	}
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d: %s", w.Code, w.Body)
	}
}

// --- "nothing to undo" returns a real status, not a bare 500 ---

func TestIntegration_UndoDowngrade_NothingScheduled(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_undo_downgrade_none_user"
		orgID = "00000000-0000-0000-0000-000000000f35"
	)
	seedActiveOrgNoSchedule(t, pool, orgID, user)

	e := billing.NewModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/downgrade/undo", ""))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d: %s", w.Code, w.Body)
	}
}

func TestIntegration_UndoAddonQuantityChange_NothingScheduled(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_undo_addon_none_user"
		orgID = "00000000-0000-0000-0000-000000000f36"
	)
	subID := seedActiveOrgNoSchedule(t, pool, orgID, user)
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO billing.subscription_addons (subscription_id, addon_id, quantity) VALUES ($1, 'extra-seat', 3)`,
		subID); err != nil {
		t.Fatalf("seed attached addon: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/addons/extra-seat/undo", ""))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d: %s", w.Code, w.Body)
	}
}

func TestIntegration_UndoCancellation_NothingScheduled(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_undo_cancel_none_user"
		orgID = "00000000-0000-0000-0000-000000000f37"
	)
	seedActiveOrgNoSchedule(t, pool, orgID, user)

	e := billing.NewModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/cancel/undo", ""))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d: %s", w.Code, w.Body)
	}
}

// TestIntegration_DowngradeSubscription_CancellationScheduled_Returns422
// confirms downgradeSubscription's cancellation-scheduled rejection reaches
// the HTTP layer as apperr.Validation (422), not a bare 500 — response.FromError
// only maps to a real status when the error is (or wraps) an *apperr.Error.
func TestIntegration_DowngradeSubscription_CancellationScheduled_Returns422(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_downgrade_cancelsched_user"
		orgID = "00000000-0000-0000-0000-000000000f38"
	)
	seedActiveOrgWithScheduledCancellation(t, pool, orgID, user)

	e := billing.NewModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/downgrade",
		`{"plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d: %s", w.Code, w.Body)
	}
}

// TestIntegration_UndoDowngrade_ResponseIncludesScheduledPlan confirms the
// returned subscription reflects the cleared schedule (scheduled_plan
// absent) so the frontend doesn't need a second GET just to know the undo
// took effect.
func TestIntegration_UndoDowngrade_ResponseIncludesScheduledPlan(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_undo_downgrade_resp_user"
		orgID = "00000000-0000-0000-0000-000000000f39"
	)
	seedActiveOrgWithScheduledDowngrade(t, pool, orgID, user)

	e := billing.NewModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, billingURL(orgID)+"/downgrade/undo", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Data struct {
			ScheduledPlan *string `json:"scheduled_plan"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.ScheduledPlan != nil {
		t.Errorf("want scheduled_plan cleared in the response, got %v", *resp.Data.ScheduledPlan)
	}
}

// --- response DTOs: scheduled-amendment fields on GET /billing and GET /billing/addons ---

func TestIntegration_GetSubscription_NothingScheduled_OmitsScheduledFields(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_get_sub_none_user"
		orgID = "00000000-0000-0000-0000-000000000f92"
	)
	seedActiveOrgNoSchedule(t, pool, orgID, user)

	e := billing.NewModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(orgID), ""))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Data struct {
			ScheduledPlan     *string    `json:"scheduled_plan"`
			ScheduledCycle    *string    `json:"scheduled_cycle"`
			ScheduledCancelAt *time.Time `json:"scheduled_cancel_at"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.ScheduledPlan != nil || resp.Data.ScheduledCycle != nil || resp.Data.ScheduledCancelAt != nil {
		t.Errorf("want all scheduled fields nil on an unscheduled subscription, got %+v", resp.Data)
	}
}

// TestIntegration_GetSubscription_ScheduledDowngrade_ReflectsPlanAndCycle
// also guards the fix this task made: scheduled_requested_at (repo-internal,
// set by the same seed that sets scheduled_plan/cycle) must never appear in
// this response — only the per-addon endpoint exposes a request timestamp.
func TestIntegration_GetSubscription_ScheduledDowngrade_ReflectsPlanAndCycle(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_get_sub_downgrade_user"
		orgID = "00000000-0000-0000-0000-000000000f93"
	)
	seedActiveOrgWithScheduledDowngrade(t, pool, orgID, user)

	e := billing.NewModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(orgID), ""))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var raw struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	var plan, cycle *string
	_ = json.Unmarshal(raw.Data["scheduled_plan"], &plan)
	_ = json.Unmarshal(raw.Data["scheduled_cycle"], &cycle)
	if plan == nil || *plan != "growth" {
		t.Errorf("want scheduled_plan=growth, got %v", plan)
	}
	if cycle == nil || *cycle != "monthly" {
		t.Errorf("want scheduled_cycle=monthly, got %v", cycle)
	}
	if _, ok := raw.Data["scheduled_cancel_at"]; ok {
		t.Errorf("want scheduled_cancel_at absent on a plan-only schedule, got %s", raw.Data["scheduled_cancel_at"])
	}
	if _, ok := raw.Data["scheduled_requested_at"]; ok {
		t.Errorf("want scheduled_requested_at never exposed on the subscription response, got %s", raw.Data["scheduled_requested_at"])
	}
}

func TestIntegration_GetSubscription_ScheduledCancellation_ReflectsCancelAt(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_get_sub_cancel_user"
		orgID = "00000000-0000-0000-0000-000000000f94"
	)
	seedActiveOrgWithScheduledCancellation(t, pool, orgID, user)

	e := billing.NewModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(orgID), ""))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp struct {
		Data struct {
			ScheduledPlan     *string    `json:"scheduled_plan"`
			ScheduledCycle    *string    `json:"scheduled_cycle"`
			ScheduledCancelAt *time.Time `json:"scheduled_cancel_at"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.ScheduledCancelAt == nil {
		t.Errorf("want scheduled_cancel_at set on a cancel-scheduled subscription, got nil")
	}
	if resp.Data.ScheduledPlan != nil || resp.Data.ScheduledCycle != nil {
		t.Errorf("want scheduled_plan/cycle nil on a cancel-only schedule, got plan=%v cycle=%v", resp.Data.ScheduledPlan, resp.Data.ScheduledCycle)
	}
}

func TestIntegration_ListAddons_ScheduledQuantityChange_OnlyThatRowPopulated(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_list_addons_sched_user"
		orgID = "00000000-0000-0000-0000-000000000f95"
	)
	seedTestAddonCatalogRow(t, pool)
	subID := seedActiveOrgNoSchedule(t, pool, orgID, user)
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO billing.subscription_addons (subscription_id, addon_id, quantity, scheduled_quantity, scheduled_requested_at)
		VALUES ($1, 'extra-seat', 5, 2, now())`, subID); err != nil {
		t.Fatalf("seed scheduled addon decrease: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO billing.subscription_addons (subscription_id, addon_id, quantity) VALUES ($1, $2, 3)`,
		subID, testAddonID); err != nil {
		t.Fatalf("seed unscheduled addon: %v", err)
	}

	e := billing.NewModuleEngine(pool, user, orgID)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, billingURL(orgID)+"/addons", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp struct {
		Data []struct {
			AddonID              string     `json:"addon_id"`
			ScheduledQuantity    *int       `json:"scheduled_quantity"`
			ScheduledRequestedAt *time.Time `json:"scheduled_requested_at"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("want 2 attached addons, got %d: %+v", len(resp.Data), resp.Data)
	}
	for _, row := range resp.Data {
		switch row.AddonID {
		case "extra-seat":
			if row.ScheduledQuantity == nil || *row.ScheduledQuantity != 2 {
				t.Errorf("extra-seat: want scheduled_quantity=2, got %v", row.ScheduledQuantity)
			}
			if row.ScheduledRequestedAt == nil {
				t.Errorf("extra-seat: want scheduled_requested_at set, got nil")
			}
		case testAddonID:
			if row.ScheduledQuantity != nil || row.ScheduledRequestedAt != nil {
				t.Errorf("%s: want no schedule, got quantity=%v requested_at=%v", testAddonID, row.ScheduledQuantity, row.ScheduledRequestedAt)
			}
		default:
			t.Errorf("unexpected addon row %q", row.AddonID)
		}
	}
}
