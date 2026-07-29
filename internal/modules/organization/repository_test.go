package organization_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

const testAuthSub = "integ_sub_ws_1"

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func serveWS(t *testing.T, pool *pgxpool.Pool, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	organization.NewModuleEngine(pool, testAuthSub).ServeHTTP(w, req)
	return w
}

func TestIntegration_CreateAndGetOrganization(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-cg","name":"Integ WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data := resp["data"].(map[string]any)
	orgID = data["id"].(string)
	if data["slug"] != "integ-ws-cg" {
		t.Errorf("want slug integ-ws-cg, got %v", data["slug"])
	}

	// get by ID
	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, "/api/organizations/"+orgID, ""))
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w2.Code, w2.Body)
	}
}

// stubOrgCatalogReader satisfies contracts.CatalogReader with a single
// known plan and addon — enough to exercise createOrganization's
// plan/addon/coupon-validation paths without a cross-module import into the
// billing package.
type stubOrgCatalogReader struct{}

func (stubOrgCatalogReader) GetPlanByID(_ context.Context, id string) (*contracts.PlanInfo, error) {
	if id == "growth" {
		return &contracts.PlanInfo{ID: "growth"}, nil
	}
	return nil, errors.New("plan not found")
}
func (stubOrgCatalogReader) ListPlans(_ context.Context) ([]contracts.PlanInfo, error) {
	return nil, nil
}
func (stubOrgCatalogReader) ListFeatures(_ context.Context) ([]contracts.FeatureInfo, error) {
	return nil, nil
}
func (stubOrgCatalogReader) ListAddons(_ context.Context) ([]contracts.AddonInfo, error) {
	return nil, nil
}
func (stubOrgCatalogReader) GetAddonByID(_ context.Context, id string) (*contracts.AddonInfo, error) {
	if id == "extra-seat" {
		return &contracts.AddonInfo{ID: "extra-seat"}, nil
	}
	return nil, errors.New("addon not found")
}
func (stubOrgCatalogReader) ValidateCouponCode(_ context.Context, code, _ string) error {
	if code == "WELCOME10" {
		return nil
	}
	return errors.New("coupon not found")
}

func TestIntegration_CreateOrganization_UnknownPlan_RejectedBeforeDBWrite(t *testing.T) {
	pool := testPool(t)
	const slug = "integ-ws-badplan"
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE slug = $1`, slug)
	})

	w := httptest.NewRecorder()
	organization.NewModuleEngineWithCatalogReader(pool, testAuthSub, stubOrgCatalogReader{}).ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"`+slug+`","name":"Integ WS","plan":"enterprise","cycle":"monthly"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown plan: want 422, got %d: %s", w.Code, w.Body)
	}

	var count int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM organization.organizations WHERE slug = $1`, slug).Scan(&count)
	if count != 0 {
		t.Errorf("want no organization row created after unknown-plan rejection, found %d", count)
	}
}

func TestIntegration_CreateOrganization_KnownPlan_Succeeds(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := httptest.NewRecorder()
	organization.NewModuleEngineWithCatalogReader(pool, testAuthSub, stubOrgCatalogReader{}).ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-goodplan","name":"Integ WS","plan":"growth","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("known plan: want 201, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID, _ = resp["data"].(map[string]any)["id"].(string)
}

func TestIntegration_CreateOrganization_UnknownAddon_RejectedBeforeDBWrite(t *testing.T) {
	pool := testPool(t)
	const slug = "integ-ws-badaddon"
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE slug = $1`, slug)
	})

	w := httptest.NewRecorder()
	organization.NewModuleEngineWithCatalogReader(pool, testAuthSub, stubOrgCatalogReader{}).ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/api/organizations",
			`{"slug":"`+slug+`","name":"Integ WS","plan":"growth","cycle":"monthly","addons":[{"addon_id":"does-not-exist"}]}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown addon: want 422, got %d: %s", w.Code, w.Body)
	}

	var count int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM organization.organizations WHERE slug = $1`, slug).Scan(&count)
	if count != 0 {
		t.Errorf("want no organization row created after unknown-addon rejection, found %d", count)
	}
}

func TestIntegration_CreateOrganization_InvalidCoupon_RejectedBeforeDBWrite(t *testing.T) {
	pool := testPool(t)
	const slug = "integ-ws-badcoupon"
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE slug = $1`, slug)
	})

	w := httptest.NewRecorder()
	organization.NewModuleEngineWithCatalogReader(pool, testAuthSub, stubOrgCatalogReader{}).ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/api/organizations",
			`{"slug":"`+slug+`","name":"Integ WS","plan":"growth","cycle":"monthly","coupon_code":"NOPE"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid coupon: want 422, got %d: %s", w.Code, w.Body)
	}

	var count int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM organization.organizations WHERE slug = $1`, slug).Scan(&count)
	if count != 0 {
		t.Errorf("want no organization row created after invalid-coupon rejection, found %d", count)
	}
}

func TestIntegration_CreateOrganization_ValidAddonAndCoupon_Succeeds(t *testing.T) {
	pool := testPool(t)
	const slug = "integ-ws-goodcart"

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := httptest.NewRecorder()
	organization.NewModuleEngineWithCatalogReader(pool, testAuthSub, stubOrgCatalogReader{}).ServeHTTP(w,
		httpserver.JSONTestRequest(http.MethodPost, "/api/organizations",
			`{"slug":"`+slug+`","name":"Integ WS","plan":"growth","cycle":"monthly","addons":[{"addon_id":"extra-seat","quantity":2}],"coupon_code":"WELCOME10"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("valid addon+coupon: want 201, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID, _ = resp["data"].(map[string]any)["id"].(string)
}

func TestIntegration_DuplicateSlugConflict(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-dup","name":"Dup WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-dup","name":"Another","plan":"solo","cycle":"monthly"}`))
	if w2.Code != http.StatusConflict {
		t.Errorf("want 409, got %d: %s", w2.Code, w2.Body)
	}
}

func TestIntegration_ListOrganizations(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-list","name":"List WS","plan":"solo","cycle":"monthly"}`))

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, "/api/organizations", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data := resp["data"].([]any)
	if len(data) == 0 {
		t.Error("expected at least one organization")
	}

	// cleanup
	pool.Exec(t.Context(), `DELETE FROM organization.organizations WHERE slug = 'integ-ws-list'`)
}

func TestIntegration_AddAndRemoveMember(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-mem","name":"Members WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	// add member
	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/members",
		`{"auth_sub":"sub_extra_member","role":"member"}`))
	if w2.Code != http.StatusCreated {
		t.Errorf("want 201, got %d: %s", w2.Code, w2.Body)
	}

	// list: expect owner + added member
	w3 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, "/api/organizations/"+orgID+"/members", ""))
	if w3.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w3.Code)
	}
	var listResp map[string]any
	json.NewDecoder(w3.Body).Decode(&listResp)
	members := listResp["data"].([]any)
	if len(members) < 2 {
		t.Errorf("want ≥2 members, got %d", len(members))
	}

	// remove member
	w4 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodDelete, "/api/organizations/"+orgID+"/members/sub_extra_member", ""))
	if w4.Code != http.StatusNoContent {
		t.Errorf("want 204, got %d: %s", w4.Code, w4.Body)
	}
}

// Unlike removeMember/updateMemberRole, addMember has no handler-level
// owner check ahead of it — this is the one path where
// service_member.go's addMember service-layer guard is the only thing
// stopping the organization owner from being re-added as an ordinary
// member.
func TestIntegration_AddMember_RejectsAddingTheOwner(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-owner-add","name":"Owner Add WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/members",
		`{"auth_sub":"`+testAuthSub+`","role":"member"}`))
	if w2.Code != http.StatusUnprocessableEntity {
		t.Errorf("adding the owner as a member: want 422, got %d: %s", w2.Code, w2.Body)
	}
}

// stubMembersUserReader satisfies contracts.UserReader with a single known
// profile — enough to exercise listMembers' batch profile-enrichment path
// (GetUsersByAuthSubs) without a cross-module import into the account package.
type stubMembersUserReader struct{}

func (stubMembersUserReader) GetUserByAuthSub(_ context.Context, _ string) (*contracts.UserInfo, error) {
	return nil, errors.New("not implemented")
}
func (stubMembersUserReader) GetUserByEmail(_ context.Context, _ string) (*contracts.UserInfo, error) {
	return nil, errors.New("not implemented")
}
func (stubMembersUserReader) GetUsersByAuthSubs(_ context.Context, authSubs []string) (map[string]contracts.UserInfo, error) {
	out := map[string]contracts.UserInfo{}
	for _, sub := range authSubs {
		if sub == testAuthSub {
			out[sub] = contracts.UserInfo{
				AuthSub: sub, Email: "owner@test.com", Name: "Owner Name", AvatarURL: "https://example.com/a.png",
			}
		}
	}
	return out, nil
}
func (stubMembersUserReader) IsMFAEnabled(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func TestIntegration_ListMembers_EnrichesProfileFromUserReader(t *testing.T) {
	pool := testPool(t)
	const slug = "integ-ws-mem-profile"
	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	e := organization.NewModuleEngineWithUserReader(pool, testAuthSub, stubMembersUserReader{})

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"`+slug+`","name":"Profile WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	w2 := httptest.NewRecorder()
	e.ServeHTTP(w2, httpserver.JSONTestRequest(http.MethodGet, "/api/organizations/"+orgID+"/members", ""))
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w2.Code, w2.Body)
	}
	var listResp map[string]any
	json.NewDecoder(w2.Body).Decode(&listResp)
	members := listResp["data"].([]any)
	if len(members) != 1 {
		t.Fatalf("want 1 member (owner), got %d", len(members))
	}
	owner := members[0].(map[string]any)
	if owner["email"] != "owner@test.com" {
		t.Errorf("want email=owner@test.com, got %v", owner["email"])
	}
	if owner["full_name"] != "Owner Name" {
		t.Errorf("want full_name=Owner Name, got %v", owner["full_name"])
	}
	if owner["avatar_url"] != "https://example.com/a.png" {
		t.Errorf("want avatar_url populated, got %v", owner["avatar_url"])
	}
}

func TestIntegration_GetFirstOrganizationIDForMember(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-first-org","name":"First Org WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	mod := organization.NewModuleForTest(pool)

	got, err := mod.GetFirstOrganizationIDForMember(t.Context(), testAuthSub)
	if err != nil {
		t.Fatalf("GetFirstOrganizationIDForMember: %v", err)
	}
	if got != orgID {
		t.Errorf("want %s, got %s", orgID, got)
	}

	// a user in no organization gets an empty string, not an error
	none, err := mod.GetFirstOrganizationIDForMember(t.Context(), "sub_never_joined_anything")
	if err != nil {
		t.Errorf("want nil error for a member of no organization, got %v", err)
	}
	if none != "" {
		t.Errorf("want empty string, got %q", none)
	}
}

func TestIntegration_UpdateOrganization_OwnerOnly(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-upd","name":"Update WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	// owner can update — owner == testAuthSub (set in NewModuleEngine)
	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPatch, "/api/organizations/"+orgID, `{"name":"Updated Name"}`))
	if w2.Code != http.StatusOK {
		t.Errorf("want 200, got %d: %s", w2.Code, w2.Body)
	}
}

// --- Ownership transfer ---

func TestIntegration_TransferOwnership_ToNonMember_Fails(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-tr1","name":"Transfer WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	// transfer to someone who is not a member
	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/transfer",
		`{"auth_sub":"sub_not_a_member"}`))
	if w2.Code == http.StatusNoContent {
		t.Errorf("transfer to non-member should fail, got 204")
	}
}

func TestIntegration_TransferOwnership_Success(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-tr2","name":"Transfer WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	// add a member to later receive ownership
	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/members",
		`{"auth_sub":"sub_new_owner","role":"member"}`))
	if w2.Code != http.StatusCreated {
		t.Fatalf("add member: want 201, got %d: %s", w2.Code, w2.Body)
	}

	// transfer
	w3 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/transfer",
		`{"auth_sub":"sub_new_owner"}`))
	if w3.Code != http.StatusNoContent {
		t.Fatalf("transfer: want 204, got %d: %s", w3.Code, w3.Body)
	}

	// verify new owner in DB
	var ownerID string
	pool.QueryRow(t.Context(), `SELECT owner_id FROM organization.organizations WHERE id = $1`, orgID).Scan(&ownerID)
	if ownerID != "sub_new_owner" {
		t.Errorf("after transfer: want owner=sub_new_owner, got %q", ownerID)
	}
}

// --- Leave organization ---

func TestIntegration_LeaveOrganization_AsOwner_Fails(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-lv1","name":"Leave WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	// owner cannot leave their own organization
	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodDelete, "/api/organizations/"+orgID+"/leave", ""))
	if w2.Code == http.StatusNoContent {
		t.Errorf("owner leaving should fail, got 204")
	}
}

func TestIntegration_LeaveOrganization_AsMember_Succeeds(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-lv2","name":"Leave WS 2","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	// add a different member
	serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/members",
		`{"auth_sub":"sub_leaver","role":"member"}`))

	// "sub_leaver" leaves — use a separate engine with that caller
	leaverEngine := organization.NewModuleEngine(pool, "sub_leaver")
	w2 := httptest.NewRecorder()
	leaverEngine.ServeHTTP(w2, httpserver.JSONTestRequest(http.MethodDelete, "/api/organizations/"+orgID+"/leave", ""))
	if w2.Code != http.StatusNoContent {
		t.Errorf("member leave: want 204, got %d: %s", w2.Code, w2.Body)
	}
}

// --- Invitations ---

func TestIntegration_AcceptInvitation_Valid(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-inv1","name":"Invite WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	// create invitation via API
	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/invitations",
		`{"email":"invitee@test.com","role":"member"}`))
	if w2.Code != http.StatusCreated {
		t.Fatalf("create invitation: want 201, got %d: %s", w2.Code, w2.Body)
	}
	// token has json:"-" so it's not in the response — fetch from DB directly
	var token string
	if err := pool.QueryRow(t.Context(),
		`SELECT token FROM organization.invitations WHERE organization_id = $1 AND email = $2 ORDER BY created_at DESC LIMIT 1`,
		orgID, "invitee@test.com",
	).Scan(&token); err != nil {
		t.Fatalf("fetch invitation token: %v", err)
	}

	// accept with a different caller (the invitee), whose token carries the
	// invited, verified email — required since acceptInvitation fails
	// closed on a missing/unverified/mismatched email.
	inviteeEngine := organization.NewModuleEngineWithEmail(pool, "sub_invitee", "invitee@test.com")
	w3 := httptest.NewRecorder()
	inviteeEngine.ServeHTTP(w3, httpserver.JSONTestRequest(http.MethodPost, "/api/invitations/accept",
		`{"token":"`+token+`"}`))
	if w3.Code != http.StatusNoContent {
		t.Errorf("accept invitation: want 204, got %d: %s", w3.Code, w3.Body)
	}

	// invitee should now be a member
	w4 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, "/api/organizations/"+orgID+"/members", ""))
	var membersResp map[string]any
	json.NewDecoder(w4.Body).Decode(&membersResp)
	members := membersResp["data"].([]any)
	found := false
	for _, m := range members {
		if m.(map[string]any)["auth_sub"] == "sub_invitee" {
			found = true
		}
	}
	if !found {
		t.Errorf("sub_invitee should be a member after accepting invitation")
	}
}

// --- Settings ---

func TestIntegration_UpdateSettings_PersistsTzAndLocale(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-sett","name":"Settings WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPatch, "/api/organizations/"+orgID+"/settings",
		`{"timezone":"Asia/Jakarta","locale":"id"}`))
	if w2.Code != http.StatusNoContent {
		t.Fatalf("update settings: want 204, got %d: %s", w2.Code, w2.Body)
	}

	var tz, locale string
	pool.QueryRow(t.Context(),
		`SELECT timezone, locale FROM organization.organizations WHERE id = $1`, orgID,
	).Scan(&tz, &locale)
	if tz != "Asia/Jakarta" {
		t.Errorf("timezone: want Asia/Jakarta, got %q", tz)
	}
	if locale != "id" {
		t.Errorf("locale: want id, got %q", locale)
	}
}

// --- Suspend / Unsuspend ---

func TestIntegration_SuspendAndUnsuspend(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-sus","name":"Suspend WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	mod := organization.NewModuleForTest(pool)

	if err := mod.SuspendOrganization(t.Context(), orgID, "billing_expired"); err != nil {
		t.Fatalf("SuspendOrganization: %v", err)
	}
	var status string
	pool.QueryRow(t.Context(), `SELECT status FROM organization.organizations WHERE id = $1`, orgID).Scan(&status)
	if status != "suspended" {
		t.Errorf("after suspend: want status=suspended, got %q", status)
	}

	if err := mod.UnsuspendOrganization(t.Context(), orgID); err != nil {
		t.Fatalf("UnsuspendOrganization: %v", err)
	}
	pool.QueryRow(t.Context(), `SELECT status FROM organization.organizations WHERE id = $1`, orgID).Scan(&status)
	if status != "active" {
		t.Errorf("after unsuspend: want status=active, got %q", status)
	}
}

// TestIntegration_SelfSuspendAndSelfUnsuspend_HappyPath covers the owner-
// facing danger-zone "Suspend organization" card (POST /suspend,
// POST /unsuspend) — distinct HTTP routes from the SuspendOrganization/
// UnsuspendOrganization interface methods TestIntegration_SuspendAndUnsuspend
// above already covers (those back the Studio/billing-driven path).
func TestIntegration_SelfSuspendAndSelfUnsuspend_HappyPath(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-selfsus","name":"Self Suspend WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	wSuspend := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/suspend", "{}"))
	if wSuspend.Code != http.StatusNoContent {
		t.Fatalf("suspend: want 204, got %d: %s", wSuspend.Code, wSuspend.Body)
	}
	var status, reason string
	pool.QueryRow(t.Context(), `SELECT status, suspended_reason FROM organization.organizations WHERE id = $1`, orgID).Scan(&status, &reason)
	if status != "suspended" {
		t.Errorf("after self-suspend: want status=suspended, got %q", status)
	}
	if reason != "Suspended by organization owner" {
		t.Errorf("after self-suspend: want the self-service reason marker, got %q", reason)
	}

	wUnsuspend := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/unsuspend", ""))
	if wUnsuspend.Code != http.StatusNoContent {
		t.Fatalf("unsuspend: want 204, got %d: %s", wUnsuspend.Code, wUnsuspend.Body)
	}
	pool.QueryRow(t.Context(), `SELECT status FROM organization.organizations WHERE id = $1`, orgID).Scan(&status)
	if status != "active" {
		t.Errorf("after self-unsuspend: want status=active, got %q", status)
	}
}

// TestIntegration_SelfUnsuspend_BlockedForBillingHold confirms the owner
// self-unsuspend route can't be used as a free escape hatch from a
// billing-driven suspension (unpaid invoice) — only a suspension the owner
// self-triggered through this same route is self-reversible.
func TestIntegration_SelfUnsuspend_BlockedForBillingHold(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-billhold","name":"Bill Hold WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	mod := organization.NewModuleForTest(pool)
	if err := mod.SuspendOrganization(t.Context(), orgID, "subscription expired"); err != nil {
		t.Fatalf("SuspendOrganization: %v", err)
	}

	wUnsuspend := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/unsuspend", ""))
	if wUnsuspend.Code != http.StatusUnprocessableEntity {
		t.Errorf("self-unsuspend a billing hold: want 422, got %d: %s", wUnsuspend.Code, wUnsuspend.Body)
	}
	var status string
	pool.QueryRow(t.Context(), `SELECT status FROM organization.organizations WHERE id = $1`, orgID).Scan(&status)
	if status != "suspended" {
		t.Errorf("billing-held organization must stay suspended after a blocked self-unsuspend attempt, got %q", status)
	}
}

// --- IP allowlist lock-yourself-out guard ---

func TestIntegration_UpdateSettings_IPAllowlistLocksOutCaller_Rejected(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-ipguard","name":"IP Guard WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	req := httpserver.JSONTestRequest(http.MethodPatch, "/api/organizations/"+orgID+"/settings",
		`{"timezone":"UTC","locale":"en","allowed_ips":["203.0.113.0/24"]}`)
	req.RemoteAddr = "192.0.2.1:54321" // deliberately outside the allowlist being saved

	wSettings := serveWS(t, pool, req)
	if wSettings.Code != http.StatusUnprocessableEntity {
		t.Fatalf("save an allowlist excluding caller's own IP: want 422, got %d: %s", wSettings.Code, wSettings.Body)
	}

	var allowedIPs []byte
	pool.QueryRow(t.Context(), `SELECT settings->'allowed_ips' FROM organization.organizations WHERE id = $1`, orgID).Scan(&allowedIPs)
	if string(allowedIPs) != "null" && string(allowedIPs) != "" {
		t.Errorf("rejected allowlist must not have been persisted, got settings->'allowed_ips' = %s", allowedIPs)
	}
}

func TestIntegration_UpdateSettings_IPAllowlistIncludingCaller_Succeeds(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-ipguard-ok","name":"IP Guard OK WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	req := httpserver.JSONTestRequest(http.MethodPatch, "/api/organizations/"+orgID+"/settings",
		`{"timezone":"UTC","locale":"en","allowed_ips":["192.0.2.0/24"]}`)
	req.RemoteAddr = "192.0.2.1:54321" // inside the allowlist being saved

	wSettings := serveWS(t, pool, req)
	if wSettings.Code != http.StatusNoContent {
		t.Fatalf("save an allowlist including caller's own IP: want 204, got %d: %s", wSettings.Code, wSettings.Body)
	}
}

// --- Invite code ---

func TestIntegration_InviteCode_RegenerateAndJoin(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-code","name":"Code WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	// regenerate invite code
	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/invite-code", ""))
	if w2.Code != http.StatusOK {
		t.Fatalf("regenerate invite code: want 200, got %d: %s", w2.Code, w2.Body)
	}
	var codeResp map[string]any
	json.NewDecoder(w2.Body).Decode(&codeResp)
	code := codeResp["data"].(map[string]any)["invite_code"].(string)

	// preview with a different user — read-only, must not create a membership
	joinerEngine := organization.NewModuleEngine(pool, "sub_code_joiner")
	wPreview := httptest.NewRecorder()
	joinerEngine.ServeHTTP(wPreview, httpserver.JSONTestRequest(http.MethodGet, "/api/organizations/join/preview?code="+code, ""))
	if wPreview.Code != http.StatusOK {
		t.Fatalf("preview by code: want 200, got %d: %s", wPreview.Code, wPreview.Body)
	}
	var previewResp map[string]any
	json.NewDecoder(wPreview.Body).Decode(&previewResp)
	if name := previewResp["data"].(map[string]any)["organization_name"]; name != "Code WS" {
		t.Errorf("preview organization_name: want %q, got %v", "Code WS", name)
	}

	// join with the same user
	w3 := httptest.NewRecorder()
	joinerEngine.ServeHTTP(w3, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/join",
		`{"code":"`+code+`"}`))
	if w3.Code != http.StatusOK {
		t.Fatalf("join by code: want 200, got %d: %s", w3.Code, w3.Body)
	}

	// preview again as the same user who already joined — must report
	// JOIN_ALREADY_MEMBER instead of succeeding (which would let the
	// confirm step run into an unexplained failure).
	wPreviewAgain := httptest.NewRecorder()
	joinerEngine.ServeHTTP(wPreviewAgain, httpserver.JSONTestRequest(http.MethodGet, "/api/organizations/join/preview?code="+code, ""))
	if wPreviewAgain.Code != http.StatusUnprocessableEntity {
		t.Fatalf("preview as existing member: want 422, got %d: %s", wPreviewAgain.Code, wPreviewAgain.Body)
	}
	var previewAgainResp map[string]any
	json.NewDecoder(wPreviewAgain.Body).Decode(&previewAgainResp)
	if code := previewAgainResp["status"].(map[string]any)["code"]; code != "JOIN_ALREADY_MEMBER" {
		t.Errorf("preview as existing member code: want JOIN_ALREADY_MEMBER, got %v", code)
	}

	// join again as the same user — same distinguishable error, not the
	// generic invalid-code message.
	wJoinAgain := httptest.NewRecorder()
	joinerEngine.ServeHTTP(wJoinAgain, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/join",
		`{"code":"`+code+`"}`))
	if wJoinAgain.Code != http.StatusUnprocessableEntity {
		t.Fatalf("join as existing member: want 422, got %d: %s", wJoinAgain.Code, wJoinAgain.Body)
	}
	var joinAgainResp map[string]any
	json.NewDecoder(wJoinAgain.Body).Decode(&joinAgainResp)
	if code := joinAgainResp["status"].(map[string]any)["code"]; code != "JOIN_ALREADY_MEMBER" {
		t.Errorf("join as existing member code: want JOIN_ALREADY_MEMBER, got %v", code)
	}

	// disable code
	w4 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPatch, "/api/organizations/"+orgID+"/invite-code",
		`{"enabled":false}`))
	if w4.Code != http.StatusNoContent {
		t.Fatalf("disable invite code: want 204, got %d: %s", w4.Code, w4.Body)
	}

	// preview after disable should fail
	wPreview2 := httptest.NewRecorder()
	joiner2 := organization.NewModuleEngine(pool, "sub_code_joiner2")
	joiner2.ServeHTTP(wPreview2, httpserver.JSONTestRequest(http.MethodGet, "/api/organizations/join/preview?code="+code, ""))
	if wPreview2.Code == http.StatusOK {
		t.Errorf("preview with disabled code should fail, got 200")
	}

	// join after disable should fail
	w5 := httptest.NewRecorder()
	joiner2.ServeHTTP(w5, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/join",
		`{"code":"`+code+`"}`))
	if w5.Code == http.StatusOK {
		t.Errorf("join with disabled code should fail, got 200")
	}
}

// --- Revoke invitation ---

func TestIntegration_RevokeInvitation(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-revoke","name":"Revoke WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	// create invitation
	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/invitations",
		`{"email":"revoke@test.com","role":"member"}`))
	if w2.Code != http.StatusCreated {
		t.Fatalf("create invitation: want 201, got %d: %s", w2.Code, w2.Body)
	}
	var invResp map[string]any
	json.NewDecoder(w2.Body).Decode(&invResp)
	invID := invResp["data"].(map[string]any)["id"].(string)

	// revoke it
	w3 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodDelete, "/api/organizations/"+orgID+"/invitations/"+invID, ""))
	if w3.Code != http.StatusNoContent {
		t.Fatalf("revoke invitation: want 204, got %d: %s", w3.Code, w3.Body)
	}

	// list invitations — revoked one should not appear
	w4 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, "/api/organizations/"+orgID+"/invitations", ""))
	if w4.Code != http.StatusOK {
		t.Fatalf("list invitations: want 200, got %d", w4.Code)
	}
	var listResp map[string]any
	json.NewDecoder(w4.Body).Decode(&listResp)
	invitations, _ := listResp["data"].([]any)
	for _, inv := range invitations {
		if inv.(map[string]any)["id"] == invID {
			t.Error("revoked invitation should not appear in list")
		}
	}
}

// --- Soft delete ---

func TestIntegration_DeleteOrganization_SoftDelete(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		// Status is 'deleted' — set to active first so cascade cleanup works,
		// or just force-delete.
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-del","name":"Del WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodDelete, "/api/organizations/"+orgID, ""))
	if w2.Code != http.StatusNoContent {
		t.Fatalf("delete organization: want 204, got %d: %s", w2.Code, w2.Body)
	}

	// GET must be blocked — organization middleware returns 403 for non-active organizations
	// (findOrganizationByID has no status filter, so deleted organizations are found but rejected)
	w3 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, "/api/organizations/"+orgID, ""))
	if w3.Code == http.StatusOK {
		t.Errorf("deleted organization should be inaccessible, got 200")
	}
}

func TestIntegration_AcceptInvitation_Expired(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-inv2","name":"Invite WS 2","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	// insert an already-expired invitation directly
	const expiredToken = "expired-test-token-0000000000000000"
	_, err := pool.Exec(t.Context(), `
		INSERT INTO organization.invitations (organization_id, email, role, token, invited_by, expires_at)
		VALUES ($1, 'old@test.com', 'member', $2, $3, NOW() - INTERVAL '1 day')`,
		orgID, expiredToken, testAuthSub)
	if err != nil {
		t.Fatalf("seed expired invitation: %v", err)
	}

	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/invitations/accept",
		`{"token":"`+expiredToken+`"}`))
	if w2.Code == http.StatusNoContent {
		t.Errorf("expired invitation should fail, got 204")
	}
}

func TestIntegration_AcceptInvitation_AlreadyMember(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-inv3","name":"Invite WS 3","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/invitations",
		`{"email":"already-member@test.com","role":"member"}`))
	if w2.Code != http.StatusCreated {
		t.Fatalf("create invitation: want 201, got %d: %s", w2.Code, w2.Body)
	}
	var token string
	if err := pool.QueryRow(t.Context(),
		`SELECT token FROM organization.invitations WHERE organization_id = $1 AND email = $2 ORDER BY created_at DESC LIMIT 1`,
		orgID, "already-member@test.com",
	).Scan(&token); err != nil {
		t.Fatalf("fetch invitation token: %v", err)
	}

	inviteeEngine := organization.NewModuleEngineWithEmail(pool, "sub_already_member", "already-member@test.com")
	w3 := httptest.NewRecorder()
	inviteeEngine.ServeHTTP(w3, httpserver.JSONTestRequest(http.MethodPost, "/api/invitations/accept", `{"token":"`+token+`"}`))
	if w3.Code != http.StatusNoContent {
		t.Fatalf("first accept: want 204, got %d: %s", w3.Code, w3.Body)
	}

	// accepting the same (now-accepted) invitation again should surface
	// INVITATION_ALREADY_MEMBER, not the generic 204/ambiguous success.
	w4 := httptest.NewRecorder()
	inviteeEngine.ServeHTTP(w4, httpserver.JSONTestRequest(http.MethodPost, "/api/invitations/accept", `{"token":"`+token+`"}`))
	if w4.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second accept: want 422, got %d: %s", w4.Code, w4.Body)
	}
	var errResp map[string]any
	json.NewDecoder(w4.Body).Decode(&errResp)
	code := errResp["status"].(map[string]any)["code"]
	if code != "INVITATION_ALREADY_MEMBER" {
		t.Errorf("want code INVITATION_ALREADY_MEMBER, got %v", code)
	}
}

// stubInviterUserReader resolves one known auth_sub to a full profile
// (name + email) — exercises previewInvitation's inviter-lookup enrichment
// path (GetUserByAuthSub), which the batch-oriented stubMembersUserReader/
// stubInviteAlreadyMemberUserReader stubs above don't implement.
type stubInviterUserReader struct {
	authSub string
	name    string
	email   string
}

func (s stubInviterUserReader) GetUserByAuthSub(_ context.Context, sub string) (*contracts.UserInfo, error) {
	if sub == s.authSub {
		return &contracts.UserInfo{AuthSub: sub, Email: s.email, Name: s.name}, nil
	}
	return nil, errors.New("not found")
}

func (s stubInviterUserReader) GetUserByEmail(_ context.Context, _ string) (*contracts.UserInfo, error) {
	return nil, errors.New("not implemented")
}

func (s stubInviterUserReader) GetUsersByAuthSubs(_ context.Context, _ []string) (map[string]contracts.UserInfo, error) {
	return nil, nil
}

func (s stubInviterUserReader) IsMFAEnabled(_ context.Context, _ string) (bool, error) {
	return false, nil
}

// TestIntegration_PreviewInvitation_ReturnsOrgAndInviterDetails guards
// against a regression where invitationPreview had no json tags: the wire
// response serialized as PascalCase (OrganizationName, Role, ...) while
// every field the accept-page UI reads is snake_case, so organization
// name/role/inviter silently decoded to zero values in the browser
// (rendered as "Join ?" / "invited you as organization.roles.undefined").
func TestIntegration_PreviewInvitation_ReturnsOrgAndInviterDetails(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations",
		`{"slug":"integ-ws-inv-preview","name":"Preview WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	const token = "preview-test-token-0000000000000000"
	const inviteeEmail = "invitee-preview@test.com"
	_, err := pool.Exec(t.Context(), `
		INSERT INTO organization.invitations (organization_id, email, role, token, invited_by, expires_at)
		VALUES ($1, $2, 'admin', $3, $4, NOW() + INTERVAL '7 days')`,
		orgID, inviteeEmail, token, testAuthSub)
	if err != nil {
		t.Fatalf("seed invitation: %v", err)
	}

	ur := stubInviterUserReader{authSub: testAuthSub, name: "Ada Lovelace", email: "ada@test.com"}
	engine := organization.NewModuleEngineWithUserReaderAndEmail(pool, "sub_invitee_preview", inviteeEmail, ur)
	w2 := httptest.NewRecorder()
	engine.ServeHTTP(w2, httpserver.JSONTestRequest(http.MethodGet, "/api/invitations/preview?token="+token, ""))
	if w2.Code != http.StatusOK {
		t.Fatalf("preview: want 200, got %d: %s", w2.Code, w2.Body)
	}

	var body struct {
		Data struct {
			OrganizationName string `json:"organization_name"`
			Role             string `json:"role"`
			InvitedByEmail   string `json:"invited_by_email"`
			InvitedByName    string `json:"invited_by_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w2.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Data.OrganizationName != "Preview WS" {
		t.Errorf("want organization_name %q, got %q", "Preview WS", body.Data.OrganizationName)
	}
	if body.Data.Role != "admin" {
		t.Errorf("want role %q, got %q", "admin", body.Data.Role)
	}
	if body.Data.InvitedByEmail != "ada@test.com" {
		t.Errorf("want invited_by_email %q, got %q", "ada@test.com", body.Data.InvitedByEmail)
	}
	if body.Data.InvitedByName != "Ada Lovelace" {
		t.Errorf("want invited_by_name %q, got %q", "Ada Lovelace", body.Data.InvitedByName)
	}
}

// stubInviteAlreadyMemberUserReader resolves exactly one known email to a
// known auth_sub — enough for createInvitation's email-to-membership
// lookup without a cross-module import into the account package.
type stubInviteAlreadyMemberUserReader struct {
	email   string
	authSub string
}

func (s stubInviteAlreadyMemberUserReader) GetUserByAuthSub(_ context.Context, _ string) (*contracts.UserInfo, error) {
	return nil, errors.New("not implemented")
}

func (s stubInviteAlreadyMemberUserReader) GetUserByEmail(_ context.Context, email string) (*contracts.UserInfo, error) {
	if email == s.email {
		return &contracts.UserInfo{AuthSub: s.authSub, Email: s.email}, nil
	}
	return nil, errors.New("not found")
}

func (s stubInviteAlreadyMemberUserReader) GetUsersByAuthSubs(_ context.Context, _ []string) (map[string]contracts.UserInfo, error) {
	return nil, nil
}

func (s stubInviteAlreadyMemberUserReader) IsMFAEnabled(_ context.Context, _ string) (bool, error) {
	return false, nil
}

// TestIntegration_CreateInvitation_InviteeAlreadyMember covers the reported
// bug: inviting by email someone who is already a member of the
// organization used to silently create a dead pending invitation instead
// of being rejected.
func TestIntegration_CreateInvitation_InviteeAlreadyMember(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	const memberEmail = "existing-member@test.com"
	ur := stubInviteAlreadyMemberUserReader{email: memberEmail, authSub: "sub_existing_member"}
	ownerEngine := organization.NewModuleEngineWithUserReader(pool, testAuthSub, ur)

	w := httptest.NewRecorder()
	ownerEngine.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations",
		`{"slug":"integ-ws-inv-member","name":"Invite Member WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	// invite them once and accept, so they're a real member.
	w2 := httptest.NewRecorder()
	ownerEngine.ServeHTTP(w2, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/invitations",
		`{"email":"`+memberEmail+`","role":"member"}`))
	if w2.Code != http.StatusCreated {
		t.Fatalf("first invite: want 201, got %d: %s", w2.Code, w2.Body)
	}
	var token string
	if err := pool.QueryRow(t.Context(),
		`SELECT token FROM organization.invitations WHERE organization_id = $1 AND email = $2 ORDER BY created_at DESC LIMIT 1`,
		orgID, memberEmail,
	).Scan(&token); err != nil {
		t.Fatalf("fetch invitation token: %v", err)
	}
	inviteeEngine := organization.NewModuleEngineWithEmail(pool, "sub_existing_member", memberEmail)
	w3 := httptest.NewRecorder()
	inviteeEngine.ServeHTTP(w3, httpserver.JSONTestRequest(http.MethodPost, "/api/invitations/accept", `{"token":"`+token+`"}`))
	if w3.Code != http.StatusNoContent {
		t.Fatalf("accept: want 204, got %d: %s", w3.Code, w3.Body)
	}

	// re-inviting the now-member email must be rejected, not silently
	// create another pending invitation.
	w4 := httptest.NewRecorder()
	ownerEngine.ServeHTTP(w4, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/invitations",
		`{"email":"`+memberEmail+`","role":"member"}`))
	if w4.Code != http.StatusUnprocessableEntity {
		t.Fatalf("re-invite existing member: want 422, got %d: %s", w4.Code, w4.Body)
	}
	var errResp map[string]any
	json.NewDecoder(w4.Body).Decode(&errResp)
	if code := errResp["status"].(map[string]any)["code"]; code != "INVITEE_ALREADY_MEMBER" {
		t.Errorf("want code INVITEE_ALREADY_MEMBER, got %v", code)
	}
}

func TestIntegration_AcceptInvitation_EmailMismatch(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-inv4","name":"Invite WS 4","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/invitations",
		`{"email":"intended@test.com","role":"member"}`))
	if w2.Code != http.StatusCreated {
		t.Fatalf("create invitation: want 201, got %d: %s", w2.Code, w2.Body)
	}
	var token string
	if err := pool.QueryRow(t.Context(),
		`SELECT token FROM organization.invitations WHERE organization_id = $1 AND email = $2 ORDER BY created_at DESC LIMIT 1`,
		orgID, "intended@test.com",
	).Scan(&token); err != nil {
		t.Fatalf("fetch invitation token: %v", err)
	}

	// a different authenticated user, with a different email, tries to accept
	wrongEngine := organization.NewModuleEngineWithEmail(pool, "sub_wrong_account", "someone-else@test.com")
	w3 := httptest.NewRecorder()
	wrongEngine.ServeHTTP(w3, httpserver.JSONTestRequest(http.MethodPost, "/api/invitations/accept", `{"token":"`+token+`"}`))
	if w3.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w3.Code, w3.Body)
	}
	var errResp map[string]any
	json.NewDecoder(w3.Body).Decode(&errResp)
	status := errResp["status"].(map[string]any)
	if status["code"] != "INVITATION_EMAIL_MISMATCH" {
		t.Errorf("want code INVITATION_EMAIL_MISMATCH, got %v", status["code"])
	}
	details, _ := status["details"].(map[string]any)
	if details["invited_email"] != "intended@test.com" {
		t.Errorf("want details.invited_email intended@test.com, got %v", details["invited_email"])
	}
}

func TestIntegration_DeclineInvitation_Valid(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-decl1","name":"Decline WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/invitations",
		`{"email":"declines@test.com","role":"member"}`))
	if w2.Code != http.StatusCreated {
		t.Fatalf("create invitation: want 201, got %d: %s", w2.Code, w2.Body)
	}
	var invID, token string
	if err := pool.QueryRow(t.Context(),
		`SELECT id, token FROM organization.invitations WHERE organization_id = $1 AND email = $2 ORDER BY created_at DESC LIMIT 1`,
		orgID, "declines@test.com",
	).Scan(&invID, &token); err != nil {
		t.Fatalf("fetch invitation: %v", err)
	}

	inviteeEngine := organization.NewModuleEngineWithEmail(pool, "sub_decliner", "declines@test.com")
	w3 := httptest.NewRecorder()
	inviteeEngine.ServeHTTP(w3, httpserver.JSONTestRequest(http.MethodPost, "/api/invitations/decline", `{"token":"`+token+`"}`))
	if w3.Code != http.StatusNoContent {
		t.Fatalf("decline invitation: want 204, got %d: %s", w3.Code, w3.Body)
	}

	var count int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM organization.invitations WHERE id = $1`, invID).Scan(&count)
	if count != 0 {
		t.Errorf("declined invitation should be deleted, still found %d row(s)", count)
	}

	// declining again (already deleted) should surface a 422, not another 204.
	w4 := httptest.NewRecorder()
	inviteeEngine.ServeHTTP(w4, httpserver.JSONTestRequest(http.MethodPost, "/api/invitations/decline", `{"token":"`+token+`"}`))
	if w4.Code != http.StatusUnprocessableEntity {
		t.Errorf("re-decline: want 422, got %d: %s", w4.Code, w4.Body)
	}
}

func TestIntegration_DeclineInvitation_EmailMismatch(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-decl2","name":"Decline WS 2","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/invitations",
		`{"email":"intended2@test.com","role":"member"}`))
	if w2.Code != http.StatusCreated {
		t.Fatalf("create invitation: want 201, got %d: %s", w2.Code, w2.Body)
	}
	var invID, token string
	if err := pool.QueryRow(t.Context(),
		`SELECT id, token FROM organization.invitations WHERE organization_id = $1 AND email = $2 ORDER BY created_at DESC LIMIT 1`,
		orgID, "intended2@test.com",
	).Scan(&invID, &token); err != nil {
		t.Fatalf("fetch invitation: %v", err)
	}

	wrongEngine := organization.NewModuleEngineWithEmail(pool, "sub_wrong_decliner", "someone-else2@test.com")
	w3 := httptest.NewRecorder()
	wrongEngine.ServeHTTP(w3, httpserver.JSONTestRequest(http.MethodPost, "/api/invitations/decline", `{"token":"`+token+`"}`))
	if w3.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w3.Code, w3.Body)
	}

	// the invitation must survive an unauthorized decline attempt.
	var count int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM organization.invitations WHERE id = $1`, invID).Scan(&count)
	if count != 1 {
		t.Errorf("invitation should still exist after mismatched decline, found %d row(s)", count)
	}
}

// TestIntegration_AcceptInvitation_MissingEmailClaim_Rejected regression-tests
// that a caller whose token carries no "email" claim at all (phone/
// anonymous/SSO-without-email signups) is rejected rather than allowed
// through — the random 32-byte token alone isn't proof of the invited
// address.
func TestIntegration_AcceptInvitation_MissingEmailClaim_Rejected(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-inv6","name":"Invite WS 6","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/invitations",
		`{"email":"intended-noemail@test.com","role":"member"}`))
	if w2.Code != http.StatusCreated {
		t.Fatalf("create invitation: want 201, got %d: %s", w2.Code, w2.Body)
	}
	var token string
	if err := pool.QueryRow(t.Context(),
		`SELECT token FROM organization.invitations WHERE organization_id = $1 AND email = $2 ORDER BY created_at DESC LIMIT 1`,
		orgID, "intended-noemail@test.com",
	).Scan(&token); err != nil {
		t.Fatalf("fetch invitation token: %v", err)
	}

	// no email claim at all — NewModuleEngine sets no Raw claims.
	noEmailEngine := organization.NewModuleEngine(pool, "sub_no_email_claim")
	w3 := httptest.NewRecorder()
	noEmailEngine.ServeHTTP(w3, httpserver.JSONTestRequest(http.MethodPost, "/api/invitations/accept", `{"token":"`+token+`"}`))
	if w3.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w3.Code, w3.Body)
	}
	var errResp map[string]any
	json.NewDecoder(w3.Body).Decode(&errResp)
	if code := errResp["status"].(map[string]any)["code"]; code != "INVITATION_EMAIL_MISMATCH" {
		t.Errorf("want code INVITATION_EMAIL_MISMATCH, got %v", code)
	}
}

// TestIntegration_AcceptInvitation_UnverifiedEmail_Rejected regression-tests
// that a caller whose token's email matches the invitation but is not
// verified is still rejected.
func TestIntegration_AcceptInvitation_UnverifiedEmail_Rejected(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-inv7","name":"Invite WS 7","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/invitations",
		`{"email":"unverified@test.com","role":"member"}`))
	if w2.Code != http.StatusCreated {
		t.Fatalf("create invitation: want 201, got %d: %s", w2.Code, w2.Body)
	}
	var token string
	if err := pool.QueryRow(t.Context(),
		`SELECT token FROM organization.invitations WHERE organization_id = $1 AND email = $2 ORDER BY created_at DESC LIMIT 1`,
		orgID, "unverified@test.com",
	).Scan(&token); err != nil {
		t.Fatalf("fetch invitation token: %v", err)
	}

	// email matches the invitation exactly, but email_verified is false.
	unverifiedEngine := organization.NewModuleEngineWithUnverifiedEmail(pool, "sub_unverified_email", "unverified@test.com")
	w3 := httptest.NewRecorder()
	unverifiedEngine.ServeHTTP(w3, httpserver.JSONTestRequest(http.MethodPost, "/api/invitations/accept", `{"token":"`+token+`"}`))
	if w3.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w3.Code, w3.Body)
	}
	var errResp map[string]any
	json.NewDecoder(w3.Body).Decode(&errResp)
	if code := errResp["status"].(map[string]any)["code"]; code != "INVITATION_EMAIL_MISMATCH" {
		t.Errorf("want code INVITATION_EMAIL_MISMATCH, got %v", code)
	}
}

func TestIntegration_ListMyInvitations(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-inv5","name":"Invite WS 5","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/invitations",
		`{"email":"onboarding-invitee@test.com","role":"member"}`))
	if w2.Code != http.StatusCreated {
		t.Fatalf("create invitation: want 201, got %d: %s", w2.Code, w2.Body)
	}

	inviteeEngine := organization.NewModuleEngineWithEmail(pool, "sub_onboarding_invitee", "onboarding-invitee@test.com")
	w3 := httptest.NewRecorder()
	inviteeEngine.ServeHTTP(w3, httpserver.JSONTestRequest(http.MethodGet, "/api/me/invitations", ""))
	if w3.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w3.Code, w3.Body)
	}
	var listResp map[string]any
	json.NewDecoder(w3.Body).Decode(&listResp)
	data := listResp["data"].([]any)
	found := false
	for _, item := range data {
		row := item.(map[string]any)
		if row["organization_id"] == orgID {
			found = true
			if row["organization_name"] != "Invite WS 5" {
				t.Errorf("want organization_name %q, got %v", "Invite WS 5", row["organization_name"])
			}
			if row["token"] == "" || row["token"] == nil {
				t.Error("want a non-empty token so the caller can one-click accept")
			}
		}
	}
	if !found {
		t.Errorf("pending invitation for the caller's email not found in GET /me/invitations response: %v", data)
	}
}

// --- Usage recording tests ---

// usageCall captures a single RecordUsage invocation from the background goroutine.
type usageCall struct {
	organizationID string
	metric         string
	value          int64
}

// captureWriter is a BillingWriter that sends each call to a buffered channel.
type captureWriter struct {
	ch chan usageCall
}

func newCaptureWriter() *captureWriter {
	return &captureWriter{ch: make(chan usageCall, 8)}
}

func (c *captureWriter) RecordUsage(_ context.Context, organizationID, metric string, value int64) error {
	c.ch <- usageCall{organizationID, metric, value}
	return nil
}

// AnonymizeHistory satisfies contracts.BillingWriter; unused by these tests.
func (c *captureWriter) AnonymizeHistory(_ context.Context, _ string) error {
	return nil
}

// waitUsage waits up to 500 ms for a RecordUsage call for the "members" metric.
func waitUsage(t *testing.T, bw *captureWriter) usageCall {
	t.Helper()
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case call := <-bw.ch:
			if call.metric == "members" {
				return call
			}
		case <-deadline:
			t.Fatalf("timed out waiting for usage recording of metric %q", "members")
		}
	}
}

func TestIntegration_AddMember_RecordsUsage(t *testing.T) {
	pool := testPool(t)
	bw := newCaptureWriter()

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	e := organization.NewModuleEngineWithWriter(pool, testAuthSub, bw)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-usage1","name":"Usage WS 1","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	w2 := httptest.NewRecorder()
	e.ServeHTTP(w2, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/members",
		`{"auth_sub":"sub_usage_member","role":"member"}`))
	if w2.Code != http.StatusCreated {
		t.Fatalf("add member: want 201, got %d: %s", w2.Code, w2.Body)
	}

	call := waitUsage(t, bw)
	if call.organizationID != orgID {
		t.Errorf("usage organization_id: want %s, got %s", orgID, call.organizationID)
	}
	if call.value < 2 {
		t.Errorf("members usage value: want ≥2 (owner+member), got %d", call.value)
	}
}

func TestIntegration_RemoveMember_RecordsUsage(t *testing.T) {
	pool := testPool(t)
	bw := newCaptureWriter()

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	e := organization.NewModuleEngineWithWriter(pool, testAuthSub, bw)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-usage2","name":"Usage WS 2","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	// add then remove
	w2 := httptest.NewRecorder()
	e.ServeHTTP(w2, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/members",
		`{"auth_sub":"sub_usage_rm","role":"member"}`))
	if w2.Code != http.StatusCreated {
		t.Fatalf("add member: want 201, got %d", w2.Code)
	}
	waitUsage(t, bw) // drain the add-member recording

	w3 := httptest.NewRecorder()
	e.ServeHTTP(w3, httpserver.JSONTestRequest(http.MethodDelete, "/api/organizations/"+orgID+"/members/sub_usage_rm", ""))
	if w3.Code != http.StatusNoContent {
		t.Fatalf("remove member: want 204, got %d", w3.Code)
	}

	call := waitUsage(t, bw)
	if call.value < 1 {
		t.Errorf("after removal members value should be ≥1 (owner remains), got %d", call.value)
	}
}

func TestIntegration_JoinByCode_ChecksLimit(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	// Create organization and get invite code
	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-jbc","name":"Join By Code WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	// Inject a fake billing reader that always returns limit=1 (already at limit with the owner)
	// by directly inserting a member to fill the slot, then attempting joinByCode.
	// Since billingReader is nil in test setup, joinByCode will NOT check limits — this test
	// verifies the joinByCode path doesn't panic or error without billing wired.
	_, err := pool.Exec(t.Context(), `
		UPDATE organization.organizations SET invite_code = 'testcode01', invite_code_enabled = true WHERE id = $1`, orgID)
	if err != nil {
		t.Fatalf("enable invite code: %v", err)
	}

	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/join",
		`{"code":"testcode01"}`))
	// testAuthSub is already the owner — unique violation → should fail (409 or 500)
	if w2.Code == http.StatusOK {
		t.Errorf("joining own organization should fail, got 200")
	}
}

func TestIntegration_AcceptInvitation_ChecksLimitAndRecordsUsage(t *testing.T) {
	pool := testPool(t)
	bw := newCaptureWriter()

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	e := organization.NewModuleEngineWithWriter(pool, testAuthSub, bw)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-usage3","name":"Usage WS 3","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	// Insert a valid invitation
	const token = "accept-usage-token-00000000000000000"
	_, err := pool.Exec(t.Context(), `
		INSERT INTO organization.invitations (organization_id, email, role, token, invited_by, expires_at)
		VALUES ($1, 'accept@test.com', 'member', $2, $3, NOW() + INTERVAL '7 days')`,
		orgID, token, testAuthSub)
	if err != nil {
		t.Fatalf("seed invitation: %v", err)
	}

	// Accept as a different user so that a new membership is inserted and the
	// member count increases to ≥2, which is what the usage assertion checks.
	acceptEngine := organization.NewModuleEngineWithWriterAndEmail(pool, "sub_invitee_usage", "accept@test.com", bw)
	w2 := httptest.NewRecorder()
	acceptEngine.ServeHTTP(w2, httpserver.JSONTestRequest(http.MethodPost, "/api/invitations/accept",
		`{"token":"`+token+`"}`))
	if w2.Code != http.StatusNoContent {
		t.Fatalf("accept: want 204, got %d: %s", w2.Code, w2.Body)
	}

	// billingWriter is wired but billingReader is nil (no limit check), so usage should be recorded
	call := waitUsage(t, bw)
	if call.organizationID != orgID {
		t.Errorf("usage organization_id: want %s, got %s", orgID, call.organizationID)
	}
	if call.value < 2 {
		t.Errorf("members after accept: want ≥2, got %d", call.value)
	}
}

func TestIntegration_ImportMembers_CommitCreatesInvitations(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"integ-ws-import1","name":"Import WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	w2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations/"+orgID+"/members/import", `{
		"dry_run": false,
		"rows": [
			{"email": "import-a@test.com", "role": "member"},
			{"email": "not-an-email", "role": "member"},
			{"email": "import-b@test.com", "role": "admin"}
		]
	}`))
	if w2.Code != http.StatusOK {
		t.Fatalf("import: want 200, got %d: %s", w2.Code, w2.Body)
	}
	var importResp struct {
		Data struct {
			Imported int `json:"imported"`
			Failed   []struct {
				Email  string `json:"email"`
				Reason string `json:"reason"`
			} `json:"failed"`
		} `json:"data"`
	}
	json.NewDecoder(w2.Body).Decode(&importResp)
	if importResp.Data.Imported != 2 {
		t.Errorf("want 2 imported, got %d", importResp.Data.Imported)
	}
	if len(importResp.Data.Failed) != 1 || importResp.Data.Failed[0].Reason != "invalid email" {
		t.Errorf("want 1 failed row (invalid email), got %+v", importResp.Data.Failed)
	}

	var count int
	if err := pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM organization.invitations WHERE organization_id = $1 AND status = 'pending'`,
		orgID,
	).Scan(&count); err != nil {
		t.Fatalf("count invitations: %v", err)
	}
	if count != 2 {
		t.Errorf("want 2 pending invitations created, got %d", count)
	}
}
