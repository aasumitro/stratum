package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	jwtgo "github.com/golang-jwt/jwt/v5"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
)

// --- RequireMFAIfEnabled ---

type stubMFAUserReader struct {
	enabled bool
	err     error
}

func (s stubMFAUserReader) GetUserByAuthSub(_ context.Context, _ string) (*contracts.UserInfo, error) {
	return nil, nil
}
func (s stubMFAUserReader) GetUserByEmail(_ context.Context, _ string) (*contracts.UserInfo, error) {
	return nil, nil
}
func (s stubMFAUserReader) GetUsersByAuthSubs(_ context.Context, _ []string) (map[string]contracts.UserInfo, error) {
	return nil, nil
}
func (s stubMFAUserReader) IsMFAEnabled(_ context.Context, _ string) (bool, error) {
	return s.enabled, s.err
}

func makeMFAGateEngine(reader contracts.UserReader, aal string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		claims := middleware.Claims{Subject: "sub_test"}
		if aal != "" {
			claims.Raw = jwtgo.MapClaims{"aal": aal}
		}
		c.Set("auth.claims", claims)
		c.Next()
	})
	e.POST("/test", middleware.RequireMFAIfEnabled(reader), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return e
}

func TestRequireMFAIfEnabled_NotEnabled_Passes(t *testing.T) {
	e := makeMFAGateEngine(stubMFAUserReader{enabled: false}, "aal1")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/test", nil))
	if w.Code != http.StatusOK {
		t.Errorf("MFA not enabled should pass regardless of aal, got %d", w.Code)
	}
}

func TestRequireMFAIfEnabled_Enabled_RejectsAAL1(t *testing.T) {
	e := makeMFAGateEngine(stubMFAUserReader{enabled: true}, "aal1")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/test", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("MFA enabled + aal1 should be rejected (403), got %d", w.Code)
	}
}

func TestRequireMFAIfEnabled_Enabled_PassesWithAAL2(t *testing.T) {
	e := makeMFAGateEngine(stubMFAUserReader{enabled: true}, "aal2")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/test", nil))
	if w.Code != http.StatusOK {
		t.Errorf("MFA enabled + aal2 should pass, got %d", w.Code)
	}
}

// TestRequireMFAIfEnabled_FailsClosed_OnLookupError regression-tests that a
// lookup error blocks the request rather than letting it through — this
// gate protects the highest-impact actions (delete account, transfer
// ownership, plan/addon changes), so a caller whose MFA status can't
// currently be verified must not be treated the same as a caller
// confirmed not to have MFA enabled.
func TestRequireMFAIfEnabled_FailsClosed_OnLookupError(t *testing.T) {
	e := makeMFAGateEngine(stubMFAUserReader{err: errors.New("db timeout")}, "aal1")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/test", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("lookup error should fail closed (503), got %d: %s", w.Code, w.Body)
	}
}

// --- RequireRole ---

func makeRoleEngine(callerRole string, allowed ...string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		if callerRole != "" {
			c.Set("organization.role", callerRole)
		}
		c.Next()
	})
	e.GET("/test", middleware.RequireRole(allowed...), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return e
}

func TestRequireRole_Allows_MatchingRole(t *testing.T) {
	e := makeRoleEngine("owner", "owner", "admin")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/test", nil))
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
}

func TestRequireRole_Rejects_WrongRole(t *testing.T) {
	e := makeRoleEngine("member", "owner", "admin")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/test", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestRequireRole_Rejects_NoRole(t *testing.T) {
	e := makeRoleEngine("", "owner")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/test", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

// --- NewOrganizationMiddleware ---

type stubWsReader struct {
	ws      *contracts.OrganizationInfo
	wsErr   error
	role    string
	roleErr error
}

func (s stubWsReader) GetOrganizationByID(_ context.Context, _ string) (*contracts.OrganizationInfo, error) {
	return s.ws, s.wsErr
}
func (s stubWsReader) GetMemberRole(_ context.Context, _, _ string) (string, error) {
	return s.role, s.roleErr
}
func (s stubWsReader) ListMemberAuthSubs(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (s stubWsReader) GetFirstOrganizationIDForMember(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (s stubWsReader) ListMembershipsForExport(_ context.Context, _ string) ([]contracts.OrgMembershipInfo, error) {
	return nil, nil
}
func (s stubWsReader) ListOwnedOrganizationIDs(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (s stubWsReader) CountActiveOwnedOrganizations(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func makeOrganizationEngine(reader contracts.OrganizationReader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: "sub_test"})
		c.Next()
	})
	orgMW := middleware.NewOrganizationMiddleware(reader)
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	e.GET("/api/organizations/:organizationID/test", orgMW, ok)
	e.POST("/api/organizations/:organizationID/unsuspend", orgMW, ok)
	// route without :organizationID for missing-ID and header-fallback tests
	e.GET("/no-param", orgMW, ok)
	return e
}

func TestOrganizationMiddleware_MissingOrganizationID(t *testing.T) {
	e := makeOrganizationEngine(stubWsReader{})
	w := httptest.NewRecorder()
	// no :organizationID param, no header → 400
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/no-param", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestOrganizationMiddleware_OrganizationNotFound(t *testing.T) {
	e := makeOrganizationEngine(stubWsReader{wsErr: errors.New("not found")})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/organizations/ws-01/test", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

// Uses "deleted", not "suspended" — a suspended organization's GET routes
// are deliberately exempt (its shell stays navigable so members can still
// see it, per isSuspensionExempt), so that status wouldn't isolate the
// active-check this test is named for. A valid role is given too, so a
// 403 can only come from the active check, not the member check below it.
func TestOrganizationMiddleware_OrganizationNotActive(t *testing.T) {
	ws := &contracts.OrganizationInfo{ID: "ws-01", Status: "deleted"}
	e := makeOrganizationEngine(stubWsReader{ws: ws, role: "member"})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/organizations/ws-01/test", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

// Confirms the active-only check does not block /unsuspend itself for a
// suspended organization — otherwise a suspended organization would be
// permanently unrecoverable via the API.
func TestOrganizationMiddleware_Suspended_UnsuspendStaysReachable(t *testing.T) {
	ws := &contracts.OrganizationInfo{ID: "ws-01", Status: "suspended"}
	e := makeOrganizationEngine(stubWsReader{ws: ws, role: "owner"})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/organizations/ws-01/unsuspend", nil))
	if w.Code != http.StatusOK {
		t.Errorf("/unsuspend on a suspended organization: want 200, got %d", w.Code)
	}
}

func TestOrganizationMiddleware_NotMember(t *testing.T) {
	ws := &contracts.OrganizationInfo{ID: "ws-01", Status: "active"}
	e := makeOrganizationEngine(stubWsReader{ws: ws, roleErr: errors.New("not a member")})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/organizations/ws-01/test", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestOrganizationMiddleware_Happy_InjectsContext(t *testing.T) {
	ws := &contracts.OrganizationInfo{ID: "ws-01", Status: "active", Name: "Test WS"}
	e := makeOrganizationEngine(stubWsReader{ws: ws, role: "member"})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/organizations/ws-01/test", nil))
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
}

func TestOrganizationMiddleware_HeaderFallback(t *testing.T) {
	ws := &contracts.OrganizationInfo{ID: "ws-header", Status: "active"}
	e := makeOrganizationEngine(stubWsReader{ws: ws, role: "admin"})
	req := httptest.NewRequest(http.MethodGet, "/no-param", nil)
	req.Header.Set("X-Organization-ID", "ws-header")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
}
