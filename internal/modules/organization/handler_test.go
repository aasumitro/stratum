package organization_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// helpers
func ownerEngine() *gin.Engine { return organization.NewHandlerEngine("sub_caller") }

// --- createOrganization ---

func TestCreateOrganization_MissingSlug(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations", `{"name":"Acme"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
}

func TestCreateOrganization_MissingName(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations", `{"slug":"acme"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
}

func TestCreateOrganization_SlugTooLong(t *testing.T) {
	slug := strings.Repeat("a", 64) // max is 63
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations",
		`{"slug":"`+slug+`","name":"Acme"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for slug >63 chars, got %d", w.Code)
	}
}

func TestCreateOrganization_NameTooLong(t *testing.T) {
	name := strings.Repeat("a", 101) // max is 100
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations",
		`{"slug":"acme","name":"`+name+`"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for name >100 chars, got %d", w.Code)
	}
}

// --- listOrganizations / getOrganization ---

func TestListOrganizations_RouteRegistered(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/organizations", ""))
	if w.Code == http.StatusNotFound {
		t.Errorf("route not registered")
	}
}

func TestGetOrganization_RouteRegistered(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/organizations/ws_01", ""))
	if w.Code == http.StatusNotFound {
		t.Errorf("route not registered")
	}
}

// --- updateOrganization ---

func TestUpdateOrganization_NonOwnerForbidden(t *testing.T) {
	w := httptest.NewRecorder()
	organization.NewHandlerEngine("sub_owner").ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/organizations/ws_01", `{"name":"New Name"}`))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestUpdateOrganization_OwnerAllowed(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/organizations/ws_01", `{"name":"New Name"}`))
	if w.Code == http.StatusForbidden {
		t.Errorf("owner should not get 403")
	}
}

func TestUpdateOrganization_EmptyBody(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/organizations/ws_01", `{}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for empty update, got %d", w.Code)
	}
}

func TestUpdateOrganization_SlugTooLong(t *testing.T) {
	slug := strings.Repeat("a", 64)
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/organizations/ws_01",
		`{"slug":"`+slug+`"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for slug >63 chars, got %d", w.Code)
	}
}

// --- deleteOrganization ---

func TestDeleteOrganization_NonOwnerForbidden(t *testing.T) {
	w := httptest.NewRecorder()
	organization.NewHandlerEngine("sub_owner").ServeHTTP(w, httpserver.JSONTestRequest(http.MethodDelete, "/organizations/ws_01", ""))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestDeleteOrganization_OwnerAllowed(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodDelete, "/organizations/ws_01", ""))
	if w.Code == http.StatusForbidden {
		t.Errorf("owner should not get 403")
	}
}

// --- updateSettings ---

func TestUpdateSettings_MissingTimezone(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/organizations/ws_01/settings",
		`{"locale":"en"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for missing timezone, got %d", w.Code)
	}
}

func TestUpdateSettings_MissingLocale(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/organizations/ws_01/settings",
		`{"timezone":"UTC"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for missing locale, got %d", w.Code)
	}
}

// A present-but-unresolvable timezone passes the binding layer (it's a
// non-empty string) but must be rejected by the service before any write —
// time.LoadLocation is the arbiter.
func TestUpdateSettings_InvalidTimezone(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/organizations/ws_01/settings",
		`{"timezone":"Not/AZone","locale":"en"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for an unresolvable timezone, got %d", w.Code)
	}
}

// locale is validated against the BCP-47 shape ^[A-Za-z]{2,3}(-[A-Za-z]{2})?$
// — "en_US!!" (underscore + punctuation) is outside it and must be rejected
// before any write.
func TestUpdateSettings_InvalidLocale(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/organizations/ws_01/settings",
		`{"timezone":"UTC","locale":"en_US!!"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for a malformed locale, got %d", w.Code)
	}
}

func TestUpdateSettings_NonOwnerForbidden(t *testing.T) {
	w := httptest.NewRecorder()
	organization.NewHandlerEngine("sub_owner").ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/organizations/ws_01/settings",
		`{"timezone":"UTC","locale":"en"}`))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

// --- suspendOrganization / unsuspendOrganization (danger zone) ---

func TestSuspendOrganization_NonOwnerForbidden(t *testing.T) {
	w := httptest.NewRecorder()
	organization.NewHandlerEngine("sub_owner").ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations/ws_01/suspend", ""))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestUnsuspendOrganization_NonOwnerForbidden(t *testing.T) {
	w := httptest.NewRecorder()
	organization.NewHandlerEngine("sub_owner").ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations/ws_01/unsuspend", ""))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

// --- transferOwnership ---

func TestTransferOwnership_MissingAuthSub(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations/ws_01/transfer", `{}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for missing auth_sub, got %d", w.Code)
	}
}

func TestTransferOwnership_NonOwnerForbidden(t *testing.T) {
	w := httptest.NewRecorder()
	organization.NewHandlerEngine("sub_owner").ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations/ws_01/transfer",
		`{"auth_sub":"sub_x"}`))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

// --- members ---

func TestListMembers_RouteRegistered(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/organizations/ws_01/members", ""))
	if w.Code == http.StatusNotFound {
		t.Errorf("route not registered")
	}
}

func TestAddMember_OwnerRoleRejected(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations/ws_01/members",
		`{"auth_sub":"sub_x","role":"owner"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
}

func TestAddMember_MissingAuthSub(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations/ws_01/members",
		`{"role":"member"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
}

func TestAddMember_MissingRole(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations/ws_01/members",
		`{"auth_sub":"sub_x"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for missing role, got %d", w.Code)
	}
}

func TestRemoveMember_CannotRemoveSelf(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodDelete, "/organizations/ws_01/members/sub_caller", ""))
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// adminEngine: caller is "sub_caller" with admin role; organization owner is "sub_owner".
func adminEngine() *gin.Engine { return organization.NewHandlerEngineWith("sub_owner", "admin") }

func TestRemoveMember_CannotRemoveOwner(t *testing.T) {
	w := httptest.NewRecorder()
	// admin (sub_caller) attempts to remove the organization owner (sub_owner)
	adminEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodDelete, "/organizations/ws_01/members/sub_owner", ""))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403 when admin tries to remove owner, got %d", w.Code)
	}
}

func TestUpdateMemberRole_CannotDemoteOwner(t *testing.T) {
	w := httptest.NewRecorder()
	// admin (sub_caller) attempts to change the organization owner's (sub_owner) role to member
	adminEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/organizations/ws_01/members/sub_owner/role",
		`{"role":"member"}`))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403 when admin tries to demote owner, got %d", w.Code)
	}
}

func TestUpdateMemberRole_OwnerRoleRejected(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/organizations/ws_01/members/sub_other/role",
		`{"role":"owner"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
}

func TestUpdateMemberRole_MissingRole(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/organizations/ws_01/members/sub_other/role",
		`{}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for missing role, got %d", w.Code)
	}
}

// --- invitations ---

func TestListInvitations_RouteRegistered(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/organizations/ws_01/invitations", ""))
	if w.Code == http.StatusNotFound {
		t.Errorf("route not registered")
	}
}

func TestCreateInvitation_MissingEmail(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations/ws_01/invitations",
		`{"role":"member"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for missing email, got %d", w.Code)
	}
}

func TestCreateInvitation_InvalidEmail(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations/ws_01/invitations",
		`{"email":"not-an-email","role":"member"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for invalid email, got %d", w.Code)
	}
}

func TestCreateInvitation_MissingRole(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations/ws_01/invitations",
		`{"email":"bob@example.com"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for missing role, got %d", w.Code)
	}
}

func TestCreateInvitation_InvalidRole(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations/ws_01/invitations",
		`{"email":"bob@example.com","role":"superuser"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for invalid role, got %d", w.Code)
	}
}

func TestAcceptInvitation_MissingToken(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/invitations/accept", `{}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for missing token, got %d", w.Code)
	}
}

func TestDeclineInvitation_MissingToken(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/invitations/decline", `{}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for missing token, got %d", w.Code)
	}
}

func TestRequestNewInvitation_MissingToken(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/invitations/request-new", `{}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for missing token, got %d", w.Code)
	}
}

func TestImportMembers_DryRun_ReportsPerRowErrors(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations/ws_01/members/import", `{
		"dry_run": true,
		"rows": [
			{"email": "sara@beta.io", "role": "member"},
			{"email": "tom@", "role": "member"},
			{"email": "li@corp.com", "role": "boss"},
			{"email": "sara@beta.io", "role": "admin"}
		]
	}`))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Data struct {
			ValidCount int `json:"valid_count"`
			Errors     []struct {
				Index  int    `json:"index"`
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.ValidCount != 1 {
		t.Errorf("want 1 valid row, got %d", resp.Data.ValidCount)
	}
	if len(resp.Data.Errors) != 3 {
		t.Fatalf("want 3 errors, got %d: %+v", len(resp.Data.Errors), resp.Data.Errors)
	}
	if resp.Data.Errors[0].Reason != "invalid email" {
		t.Errorf("row 1: want 'invalid email', got %q", resp.Data.Errors[0].Reason)
	}
	if resp.Data.Errors[1].Reason != `unknown role "boss"` {
		t.Errorf("row 2: want unknown-role reason, got %q", resp.Data.Errors[1].Reason)
	}
	if resp.Data.Errors[2].Reason != "duplicate row" {
		t.Errorf("row 3: want 'duplicate row', got %q", resp.Data.Errors[2].Reason)
	}
}

func TestImportMembers_DryRun_NeverTouchesService(t *testing.T) {
	// nil-service test engine — this would panic if importMembers reached
	// h.svc in the dry_run branch.
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations/ws_01/members/import",
		`{"dry_run": true, "rows": [{"email": "a@b.com", "role": "member"}]}`))
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d: %s", w.Code, w.Body)
	}
}

func TestListMyInvitations_RouteRegistered(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/me/invitations", ""))
	if w.Code == http.StatusNotFound {
		t.Errorf("route not registered")
	}
}

// --- invite code ---

func TestToggleInviteCode_NonOwnerForbidden(t *testing.T) {
	w := httptest.NewRecorder()
	organization.NewHandlerEngine("sub_owner").ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/organizations/ws_01/invite-code",
		`{"enabled":true}`))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

// --- joinByCode ---

func TestJoinByCode_MissingCode(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/organizations/join", `{}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for missing code, got %d", w.Code)
	}
}

// --- previewInviteCode ---

func TestPreviewInviteCode_MissingCode(t *testing.T) {
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/organizations/join/preview", ""))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for missing code, got %d", w.Code)
	}
}

// --- auditLog ---

func TestAuditLog_RouteRegistered(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/organizations/ws_01/audit-log", ""))
	if w.Code == http.StatusNotFound {
		t.Errorf("route not registered")
	}
}

func TestExportAuditLog_SetsCSVContentType(t *testing.T) {
	w := httptest.NewRecorder()
	defer func() {
		recover()
		ct := w.Header().Get("Content-Type")
		if ct != "text/csv" {
			t.Errorf("want Content-Type text/csv, got %q", ct)
		}
	}()
	ownerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/organizations/ws_01/audit-log/export", ""))
}
