package account_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/account"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

const testAuthSub = "integ_sub_profile_1"

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

func serve(t *testing.T, pool *pgxpool.Pool, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	account.NewModuleEngine(pool, testAuthSub).ServeHTTP(w, req)
	return w
}

// TestIntegration_IsMFAEnabled_NoProfileRow_NotAnError regression-tests that
// a caller with no account.users row yet resolves to (false, nil), not an
// error — a nonexistent profile is a definitive "no MFA enrolled," not an
// ambiguous lookup failure the MFA gate should fail closed on.
func TestIntegration_IsMFAEnabled_NoProfileRow_NotAnError(t *testing.T) {
	pool := testPool(t)
	const noProfileSub = "integ_sub_no_profile_ever"
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, noProfileSub)
	})

	mod := account.NewModuleForTest(pool)
	enabled, err := mod.IsMFAEnabled(t.Context(), noProfileSub)
	if err != nil {
		t.Fatalf("want no error for a caller with no profile row, got %v", err)
	}
	if enabled {
		t.Error("want enabled=false for a caller with no profile row")
	}
}

func TestIntegration_UpsertProfile_CreatesAndReturnsUser(t *testing.T) {
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, testAuthSub)
	})

	// email now comes from the caller's verified JWT claim, not the request
	// body — the body's "email" field is accepted but ignored.
	e := account.NewModuleEngineWithEmail(pool, testAuthSub, "integ@test.com")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/api/me", `{"email":"ignored@test.com","full_name":"Integ User"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data := resp["data"].(map[string]any)
	if data["email"] != "integ@test.com" {
		t.Errorf("want integ@test.com (from the JWT claim, not the ignored body field), got %v", data["email"])
	}
}

func TestIntegration_UpdateProfile_PreservesEmptyAvatarURL(t *testing.T) {
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, testAuthSub)
	})

	// seed with avatar
	serve(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/me", `{"email":"i@t.com","full_name":"I","avatar_url":"https://example.com/orig-avatar.png"}`))

	// PATCH with empty avatar_url — COALESCE(NULLIF(...)) preserves original
	w := serve(t, pool, httpserver.JSONTestRequest(http.MethodPatch, "/api/me", `{"full_name":"I Updated"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data := resp["data"].(map[string]any)
	if data["avatar_url"] != "https://example.com/orig-avatar.png" {
		t.Errorf("avatar should be preserved, got %v", data["avatar_url"])
	}
}

// --- Get profile ---

func TestIntegration_GetProfile_ReturnsUser(t *testing.T) {
	const sub = "integ_profile_get"
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, sub)
	})

	e := account.NewModuleEngineWithEmail(pool, sub, "get@test.com")
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, "/api/me", `{"full_name":"Get User"}`))

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/api/me", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("get profile: want 200, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data := resp["data"].(map[string]any)
	if data["email"] != "get@test.com" {
		t.Errorf("want email=get@test.com, got %v", data["email"])
	}
}

// --- Task list + detail ---

func TestIntegration_ListAndGetTask(t *testing.T) {
	const sub = "integ_profile_tasks"
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.tasks WHERE auth_sub = $1`, sub)
		pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, sub)
	})

	e := account.NewModuleEngine(pool, sub)
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, "/api/me", `{"email":"tasks@test.com"}`))

	// trigger delete-account (creates task)
	wDel := httptest.NewRecorder()
	e.ServeHTTP(wDel, httpserver.JSONTestRequest(http.MethodDelete, "/api/me", ""))
	if wDel.Code != http.StatusAccepted {
		t.Fatalf("request delete: want 202, got %d: %s", wDel.Code, wDel.Body)
	}
	var delResp map[string]any
	json.NewDecoder(wDel.Body).Decode(&delResp)
	taskID := delResp["data"].(map[string]any)["id"].(string)

	// list tasks
	wList := httptest.NewRecorder()
	e.ServeHTTP(wList, httpserver.JSONTestRequest(http.MethodGet, "/api/me/tasks", ""))
	if wList.Code != http.StatusOK {
		t.Fatalf("list tasks: want 200, got %d: %s", wList.Code, wList.Body)
	}
	var listResp map[string]any
	json.NewDecoder(wList.Body).Decode(&listResp)
	tasks := listResp["data"].([]any)
	if len(tasks) == 0 {
		t.Fatal("expected at least one task in list")
	}
	found := false
	for _, task := range tasks {
		if task.(map[string]any)["id"] == taskID {
			found = true
		}
	}
	if !found {
		t.Errorf("created task %q not found in list", taskID)
	}

	// get task by ID
	wGet := httptest.NewRecorder()
	e.ServeHTTP(wGet, httpserver.JSONTestRequest(http.MethodGet, "/api/me/tasks/"+taskID, ""))
	if wGet.Code != http.StatusOK {
		t.Fatalf("get task: want 200, got %d: %s", wGet.Code, wGet.Body)
	}
	var getResp map[string]any
	json.NewDecoder(wGet.Body).Decode(&getResp)
	if getResp["data"].(map[string]any)["id"] != taskID {
		t.Errorf("get task: want id=%s, got %v", taskID, getResp["data"].(map[string]any)["id"])
	}
}

// TestIntegration_GetTask_ScopedToOwnAuthSub regression-tests that a task
// belonging to one auth_sub is invisible to a request authenticated as a
// different one — findTask's WHERE clause pairs id with auth_sub, so a
// foreign task ID must 404, not return another user's task.
func TestIntegration_GetTask_ScopedToOwnAuthSub(t *testing.T) {
	const otherSub = "integ_sub_tasks_other"
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.tasks WHERE auth_sub = $1`, testAuthSub)
		pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, testAuthSub)
		pool.Exec(context.Background(), `DELETE FROM account.tasks WHERE auth_sub = $1`, otherSub)
		pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, otherSub)
	})

	eOwner := account.NewModuleEngine(pool, testAuthSub)
	eOwner.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, "/api/me", `{"email":"tasks-scoped@test.com"}`))

	wDel := httptest.NewRecorder()
	eOwner.ServeHTTP(wDel, httpserver.JSONTestRequest(http.MethodDelete, "/api/me", ""))
	if wDel.Code != http.StatusAccepted {
		t.Fatalf("request delete: want 202, got %d: %s", wDel.Code, wDel.Body)
	}
	var delResp map[string]any
	json.NewDecoder(wDel.Body).Decode(&delResp)
	taskID := delResp["data"].(map[string]any)["id"].(string)

	// a different caller tries to fetch it by ID — must not see it
	eOther := account.NewModuleEngine(pool, otherSub)
	wGet := httptest.NewRecorder()
	eOther.ServeHTTP(wGet, httpserver.JSONTestRequest(http.MethodGet, "/api/me/tasks/"+taskID, ""))
	if wGet.Code != http.StatusNotFound {
		t.Fatalf("get task (other user): want 404, got %d: %s", wGet.Code, wGet.Body)
	}
}

// --- Account deletion pre-check ---

func TestIntegration_DeleteAccount_WithOwnedOrganization_Fails(t *testing.T) {
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, testAuthSub)
		pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE owner_id = $1`, testAuthSub)
	})

	// create a profile
	serve(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/me", `{"email":"del@test.com"}`))

	// create an organization that testAuthSub owns (insert directly to avoid cross-module HTTP call)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO organization.organizations (slug, name, owner_id, status)
		VALUES ('del-test-ws', 'Del Test WS', $1, 'active')`, testAuthSub)
	if err != nil {
		t.Fatalf("seed organization: %v", err)
	}

	// delete account should fail: still owns an organization — handler returns 422
	w := serve(t, pool, httpserver.JSONTestRequest(http.MethodDelete, "/api/me", ""))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("delete with owned organization: want 422, got %d: %s", w.Code, w.Body)
	}
}

func TestIntegration_DeleteAccount_NoOwnedOrganization_CreatesTask(t *testing.T) {
	const noWsSub = "integ_profile_delete_no_ws"
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.tasks WHERE auth_sub = $1`, noWsSub)
		pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, noWsSub)
	})

	e := account.NewModuleEngine(pool, noWsSub)
	// create profile first
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, "/api/me", `{"email":"nodws@test.com"}`))

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodDelete, "/api/me", ""))
	if w.Code != http.StatusAccepted {
		t.Fatalf("delete with no organizations: want 202, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	task := resp["data"].(map[string]any)
	if task["status"] != "pending" {
		t.Errorf("task should be pending, got %v", task["status"])
	}
}
