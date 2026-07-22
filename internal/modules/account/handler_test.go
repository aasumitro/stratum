package account_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aasumitro/stratum/internal/modules/account"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// --- upsertProfile ---

// TestUpsertProfile_NoEmailClaim_Rejected regression-tests that a caller
// whose JWT carries no "email" claim is rejected rather than allowed
// through with an empty email. Email is sourced from the verified claim,
// never the request body, so NewHandlerEngine (which sets no Raw claims)
// exercises exactly this case.
func TestUpsertProfile_NoEmailClaim_Rejected(t *testing.T) {
	w := httptest.NewRecorder()
	account.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/me", `{"full_name":"Alice"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if code := resp["status"].(map[string]any)["code"]; code != "EMAIL_REQUIRED" {
		t.Errorf("want code EMAIL_REQUIRED, got %v", code)
	}
}

func TestUpsertProfile_FullNameTooLong(t *testing.T) {
	name := strings.Repeat("a", 256) // max 255
	w := httptest.NewRecorder()
	account.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/me",
		`{"email":"alice@example.com","full_name":"`+name+`"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for full_name >255 chars, got %d", w.Code)
	}
}

func TestUpsertProfile_AvatarURLInvalid(t *testing.T) {
	w := httptest.NewRecorder()
	account.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/me",
		`{"email":"alice@example.com","avatar_url":"not-a-url"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for invalid avatar_url, got %d", w.Code)
	}
}

func TestUpsertProfile_AvatarURLTooLong(t *testing.T) {
	url := "https://example.com/" + strings.Repeat("a", 2048) // max 2048 total
	w := httptest.NewRecorder()
	account.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/me",
		`{"email":"alice@example.com","avatar_url":"`+url+`"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for avatar_url >2048 chars, got %d", w.Code)
	}
}

// --- updateProfile ---

func TestUpdateProfile_FullNameTooLong(t *testing.T) {
	name := strings.Repeat("a", 256)
	w := httptest.NewRecorder()
	account.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/me",
		`{"full_name":"`+name+`"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for full_name >255 chars, got %d", w.Code)
	}
}

func TestUpdateProfile_AvatarURLInvalid(t *testing.T) {
	w := httptest.NewRecorder()
	account.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/me",
		`{"avatar_url":"not-a-url"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for invalid avatar_url, got %d", w.Code)
	}
}

func TestUpdateProfile_ValidEmptyBody(t *testing.T) {
	// omitempty fields — empty body is technically valid (no mandatory fields)
	// reaches service (nil), expect panic not 422
	defer func() { recover() }()
	w := httptest.NewRecorder()
	account.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/me", `{}`))
	if w.Code == http.StatusUnprocessableEntity {
		t.Errorf("empty body should pass validation, got 422")
	}
}

// --- updatePreferences ---

func TestUpdatePreferences_Array(t *testing.T) {
	w := httptest.NewRecorder()
	account.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/me/preferences", `[1,2,3]`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for array preferences, got %d", w.Code)
	}
}

func TestUpdatePreferences_StringValue(t *testing.T) {
	w := httptest.NewRecorder()
	account.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/me/preferences", `"hello"`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for string preferences, got %d", w.Code)
	}
}

func TestUpdatePreferences_ValidObject(t *testing.T) {
	// valid object — reaches service (nil), expect panic not 422
	defer func() { recover() }()
	w := httptest.NewRecorder()
	account.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/me/preferences",
		`{"theme":"dark","lang":"en"}`))
	if w.Code == http.StatusUnprocessableEntity {
		t.Errorf("valid object should pass validation, got 422")
	}
}

// --- handleSupabaseUserUpdated ---
// nil pool is safe here: every case below returns before touching s.pool
// (wrong secret, bad payload, or a no-op branch) — none reach syncEmail.

func TestSupabaseUserUpdated_WrongSecret(t *testing.T) {
	e := account.NewWebhookModuleEngine(nil, "Test1234")
	req := httpserver.JSONTestRequest(http.MethodPost, "/webhooks/supabase/user-updated", `{}`)
	req.Header.Set("X-Webhook-Secret", "wrong")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401 for wrong secret, got %d", w.Code)
	}
}

func TestSupabaseUserUpdated_MissingSecret(t *testing.T) {
	e := account.NewWebhookModuleEngine(nil, "Test1234")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/webhooks/supabase/user-updated", `{}`))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401 for missing secret, got %d", w.Code)
	}
}

func TestSupabaseUserUpdated_InvalidPayload(t *testing.T) {
	e := account.NewWebhookModuleEngine(nil, "Test1234")
	req := httpserver.JSONTestRequest(http.MethodPost, "/webhooks/supabase/user-updated", `not-json`)
	req.Header.Set("X-Webhook-Secret", "Test1234")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid payload, got %d", w.Code)
	}
}

func TestSupabaseUserUpdated_WrongTable_NoOp(t *testing.T) {
	e := account.NewWebhookModuleEngine(nil, "Test1234")
	body := `{"table":"identities","record":{"id":"u1","email":"new@example.com"},"old_record":{"email":"old@example.com"}}`
	req := httpserver.JSONTestRequest(http.MethodPost, "/webhooks/supabase/user-updated", body)
	req.Header.Set("X-Webhook-Secret", "Test1234")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("want 200 no-op for non-users table, got %d", w.Code)
	}
}

func TestSupabaseUserUpdated_EmailUnchanged_NoOp(t *testing.T) {
	e := account.NewWebhookModuleEngine(nil, "Test1234")
	body := `{"table":"users","record":{"id":"u1","email":"same@example.com"},"old_record":{"email":"same@example.com"}}`
	req := httpserver.JSONTestRequest(http.MethodPost, "/webhooks/supabase/user-updated", body)
	req.Header.Set("X-Webhook-Secret", "Test1234")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("want 200 no-op for unchanged email, got %d", w.Code)
	}
}

func TestSupabaseUserUpdated_EmptyConfigSecret_BypassesCheck(t *testing.T) {
	// dev-mode convenience, mirrors billing's verifyXenditToken/verifyStripeSignature —
	// still hits the wrong-table no-op branch so it never touches a nil pool.
	e := account.NewWebhookModuleEngine(nil, "")
	body := `{"table":"identities","record":{"id":"u1","email":"new@example.com"},"old_record":{"email":"old@example.com"}}`
	req := httpserver.JSONTestRequest(http.MethodPost, "/webhooks/supabase/user-updated", body)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("want 200 when no secret configured, got %d", w.Code)
	}
}

// --- recordPasswordChanged ---
// nil service is safe here: the handler never calls h.svc.

func TestRecordPasswordChanged_EmptyBody_Accepted(t *testing.T) {
	w := httptest.NewRecorder()
	account.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/me/password/changed", ""))
	if w.Code != http.StatusNoContent {
		t.Errorf("want 204 for empty body, got %d: %s", w.Code, w.Body)
	}
}

func TestRecordPasswordChanged_NonEmptyBody_Rejected(t *testing.T) {
	// Anything sent here — even {} — must be refused, not silently accepted:
	// this endpoint must never become a place sensitive data could be sent.
	w := httptest.NewRecorder()
	account.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/me/password/changed", `{"password":"whatever"}`))
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for a non-empty body, got %d: %s", w.Code, w.Body)
	}
}
