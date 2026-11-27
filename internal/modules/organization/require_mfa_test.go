package organization_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// mfaUserReader reports mem_1 as enrolled in MFA and everyone else as not.
type mfaUserReader struct{}

func (mfaUserReader) GetUserByAuthSub(context.Context, string) (*contracts.UserInfo, error) {
	return nil, errors.New("not implemented")
}
func (mfaUserReader) GetUserByEmail(context.Context, string) (*contracts.UserInfo, error) {
	return nil, errors.New("not implemented")
}
func (mfaUserReader) GetUsersByAuthSubs(_ context.Context, subs []string) (map[string]contracts.UserInfo, error) {
	out := map[string]contracts.UserInfo{}
	for _, s := range subs {
		out[s] = contracts.UserInfo{AuthSub: s, Email: s + "@example.com", MFAEnabled: s == "mem_1"}
	}
	return out, nil
}
func (mfaUserReader) IsMFAEnabled(context.Context, string) (bool, error) { return false, nil }

func serveJSON(engine http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httpserver.JSONTestRequest(method, path, body))
	return w
}

// The owner can require MFA only from an aal2 session; once on, an aal1
// member is refused everywhere but leave, and omitting the field keeps it.
func TestIntegration_RequireMFA_Policy(t *testing.T) {
	pool := testPool(t)
	orgID := setupOrgWithMembers(t, pool, "test-require-mfa-policy")
	cleanupSuspendOrg(t, pool, orgID)
	base := "/api/organizations/" + orgID
	ownerAAL1 := organization.NewModuleEngineWithSession(pool, "owner_sub", "aal1", nil)
	ownerAAL2 := organization.NewModuleEngineWithSession(pool, "owner_sub", "aal2", nil)
	memberAAL1 := organization.NewModuleEngineWithSession(pool, "mem_2", "aal1", nil)
	memberAAL2 := organization.NewModuleEngineWithSession(pool, "mem_2", "aal2", nil)

	on := `{"timezone":"UTC","locale":"en","require_mfa":true}`
	if w := serveJSON(ownerAAL1, http.MethodPatch, base+"/settings", on); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "MFA_ENROLLMENT_REQUIRED") {
		t.Fatalf("enable from aal1: want 422 MFA_ENROLLMENT_REQUIRED, got %d %s", w.Code, w.Body)
	}
	if w := serveJSON(memberAAL1, http.MethodGet, base, ""); w.Code != http.StatusOK {
		t.Fatalf("policy off: member aal1 want 200, got %d", w.Code)
	}
	if w := serveJSON(ownerAAL2, http.MethodPatch, base+"/settings", on); w.Code != http.StatusNoContent {
		t.Fatalf("enable from aal2: want 204, got %d %s", w.Code, w.Body)
	}
	if w := serveJSON(ownerAAL2, http.MethodPatch, base+"/settings", `{"timezone":"UTC","locale":"en"}`); w.Code != http.StatusNoContent {
		t.Fatalf("patch without require_mfa: want 204, got %d %s", w.Code, w.Body)
	}
	var stored string
	pool.QueryRow(t.Context(), `SELECT settings->>'require_mfa' FROM organization.organizations WHERE id = $1`, orgID).Scan(&stored)
	if stored != "true" {
		t.Fatalf("require_mfa: want kept true, got %q", stored)
	}

	if w := serveJSON(memberAAL1, http.MethodGet, base, ""); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "ORGANIZATION_MFA_REQUIRED") {
		t.Errorf("policy on: member aal1 want 403 ORGANIZATION_MFA_REQUIRED, got %d %s", w.Code, w.Body)
	}
	if w := serveJSON(memberAAL2, http.MethodGet, base, ""); w.Code != http.StatusOK {
		t.Errorf("policy on: member aal2 want 200, got %d", w.Code)
	}
	if w := serveJSON(memberAAL1, http.MethodDelete, base+"/leave", ""); w.Code != http.StatusNoContent {
		t.Errorf("policy on: leave must stay open for aal1, want 204, got %d %s", w.Code, w.Body)
	}

	if w := serveJSON(ownerAAL2, http.MethodPatch, base+"/settings", `{"timezone":"UTC","locale":"en","require_mfa":false}`); w.Code != http.StatusNoContent {
		t.Fatalf("disable: want 204, got %d %s", w.Code, w.Body)
	}
	other := organization.NewModuleEngineWithSession(pool, "mem_3", "aal1", nil)
	if w := serveJSON(other, http.MethodGet, base, ""); w.Code != http.StatusOK {
		t.Errorf("policy off again: member aal1 want 200, got %d", w.Code)
	}
}

// Owners and admins see each member's MFA status; members do not.
func TestIntegration_ListMembers_MFAStatusForOwnerAndAdminOnly(t *testing.T) {
	pool := testPool(t)
	orgID := setupOrgWithMembers(t, pool, "test-members-mfa-field")
	cleanupSuspendOrg(t, pool, orgID)
	seedOrgMember(t, pool, orgID, "admin_sub", "admin")
	path := "/api/organizations/" + orgID + "/members"

	read := func(sub string) map[string]any {
		t.Helper()
		w := serveJSON(organization.NewModuleEngineWithSession(pool, sub, "aal1", mfaUserReader{}), http.MethodGet, path, "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s list members: want 200, got %d %s", sub, w.Code, w.Body)
		}
		var resp struct {
			Data []map[string]any `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		out := map[string]any{}
		for _, m := range resp.Data {
			out[m["auth_sub"].(string)] = m["mfa_enabled"]
		}
		return out
	}

	for _, viewer := range []string{"owner_sub", "admin_sub"} {
		got := read(viewer)
		if got["mem_1"] != true || got["mem_2"] != false {
			t.Errorf("%s view: want mem_1 true, mem_2 false, got %v", viewer, got)
		}
	}
	for sub, v := range read("mem_2") {
		if v != nil {
			t.Errorf("member view: want no mfa_enabled, got %s=%v", sub, v)
		}
	}
}
