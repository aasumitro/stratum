package account_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/account"
	"github.com/aasumitro/stratum/internal/platform/cache"
	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

func encodeUserTaskEvent(routingKey, taskID, authSub string) []byte {
	env := events.Envelope{
		ID:   "task-" + taskID,
		Type: routingKey,
		Data: events.UserTaskRequest{TaskID: taskID, AuthSub: authSub},
	}
	b, _ := json.Marshal(env)
	return b
}

func TestIntegration_DeleteAccount_Worker_CompletesTask(t *testing.T) {
	const authSub = "integ_profile_delete_worker"
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.tasks WHERE auth_sub = $1`, authSub)
		pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, authSub)
	})

	e := account.NewModuleEngine(pool, authSub)
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, "/api/me", `{"email":"delete-worker@test.com"}`))

	wDel := httptest.NewRecorder()
	e.ServeHTTP(wDel, httpserver.JSONTestRequest(http.MethodDelete, "/api/me", ""))
	if wDel.Code != http.StatusAccepted {
		t.Fatalf("request delete: want 202, got %d: %s", wDel.Code, wDel.Body)
	}

	var resp map[string]any
	json.NewDecoder(wDel.Body).Decode(&resp)
	taskID := resp["data"].(map[string]any)["id"].(string)

	mod := account.NewModuleForTest(pool)
	if err := mod.Worker.HandleDeleteAccount(t.Context(), encodeUserTaskEvent(events.RoutingKeyUserDeleteRequest, taskID, authSub)); err != nil {
		t.Fatalf("HandleDeleteAccount: %v", err)
	}

	var status string
	pool.QueryRow(t.Context(), `SELECT status FROM account.tasks WHERE id = $1`, taskID).Scan(&status)
	if status != "completed" {
		t.Errorf("task status: want completed, got %q", status)
	}

	var count int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM account.users WHERE auth_sub = $1`, authSub).Scan(&count)
	if count != 0 {
		t.Errorf("user should be deleted after worker runs, but still exists")
	}
}

// erroringWriter implements contracts.OrganizationWriter, contracts.NotificationWriter,
// and contracts.BillingWriter, always failing — used to verify that a cleanup-step
// failure during account deletion is recorded on the task instead of silently discarded.
type erroringWriter struct{}

func (erroringWriter) RemoveAllMemberships(context.Context, string) error { return errors.New("boom") }
func (erroringWriter) DeleteAllForUser(context.Context, string) error     { return errors.New("boom") }
func (erroringWriter) RecordUsage(context.Context, string, string, int64) error {
	return errors.New("boom")
}
func (erroringWriter) AnonymizeHistory(context.Context, string) error { return errors.New("boom") }

func TestIntegration_DeleteAccount_Worker_RecordsFailedCleanupSteps(t *testing.T) {
	const authSub = "integ_profile_delete_worker_partial"
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.tasks WHERE auth_sub = $1`, authSub)
		pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, authSub)
	})

	e := account.NewModuleEngine(pool, authSub)
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, "/api/me", `{"email":"delete-worker-partial@test.com"}`))

	wDel := httptest.NewRecorder()
	e.ServeHTTP(wDel, httpserver.JSONTestRequest(http.MethodDelete, "/api/me", ""))
	if wDel.Code != http.StatusAccepted {
		t.Fatalf("request delete: want 202, got %d: %s", wDel.Code, wDel.Body)
	}

	var resp map[string]any
	json.NewDecoder(wDel.Body).Decode(&resp)
	taskID := resp["data"].(map[string]any)["id"].(string)

	mod := account.NewModuleForTest(pool)
	mod.SetOrganizationWriter(erroringWriter{})
	mod.SetNotificationWriter(erroringWriter{})
	mod.SetBillingWriter(erroringWriter{})
	if err := mod.Worker.HandleDeleteAccount(t.Context(), encodeUserTaskEvent(events.RoutingKeyUserDeleteRequest, taskID, authSub)); err != nil {
		t.Fatalf("HandleDeleteAccount: %v", err)
	}

	var status string
	var result []byte
	pool.QueryRow(t.Context(), `SELECT status, result FROM account.tasks WHERE id = $1`, taskID).Scan(&status, &result)
	if status != "completed" {
		t.Fatalf("task status: want completed (best-effort), got %q", status)
	}

	var parsed struct {
		Deleted     bool     `json:"deleted"`
		FailedSteps []string `json:"failed_steps"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("unmarshal task result: %v", err)
	}
	wantFailed := []string{"notifications", "memberships", "billing_history"}
	if len(parsed.FailedSteps) != len(wantFailed) {
		t.Fatalf("failed_steps: want %v, got %v", wantFailed, parsed.FailedSteps)
	}
	for _, step := range wantFailed {
		if !slices.Contains(parsed.FailedSteps, step) {
			t.Errorf("failed_steps missing %q, got %v", step, parsed.FailedSteps)
		}
	}
}

// fakeSupabaseAdmin stands in for Supabase's GET /admin/users/:id endpoint,
// returning a fixed set of MFA factors for any request.
func fakeSupabaseAdmin(factors string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"factors":` + factors + `}`))
	}))
}

func TestIntegration_SyncMFAStatus_VerifiedFactor_SetsEnabled(t *testing.T) {
	const authSub = "integ_profile_mfa_enabled"
	pool := testPool(t)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, authSub) })

	admin := fakeSupabaseAdmin(`[{"status":"verified"}]`)
	defer admin.Close()

	e := account.NewModuleEngineWithAdmin(pool, authSub, admin.URL, "test-service-role-key")
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, "/api/me", `{"email":"mfa-on@test.com"}`))

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/api/me/mfa/sync", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("sync mfa status: want 200, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if enabled, _ := resp["data"].(map[string]any)["mfa_enabled"].(bool); !enabled {
		t.Errorf("response mfa_enabled: want true, got %v", resp["data"])
	}

	var dbEnabled bool
	pool.QueryRow(t.Context(), `SELECT mfa_enabled FROM account.users WHERE auth_sub = $1`, authSub).Scan(&dbEnabled)
	if !dbEnabled {
		t.Error("account.users.mfa_enabled: want true after sync with a verified factor")
	}
}

func TestIntegration_SyncMFAStatus_NoVerifiedFactor_SetsDisabled(t *testing.T) {
	const authSub = "integ_profile_mfa_disabled"
	pool := testPool(t)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, authSub) })

	admin := fakeSupabaseAdmin(`[{"status":"unverified"}]`)
	defer admin.Close()

	e := account.NewModuleEngineWithAdmin(pool, authSub, admin.URL, "test-service-role-key")
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, "/api/me", `{"email":"mfa-off@test.com"}`))

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPost, "/api/me/mfa/sync", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("sync mfa status: want 200, got %d: %s", w.Code, w.Body)
	}

	var dbEnabled bool
	pool.QueryRow(t.Context(), `SELECT mfa_enabled FROM account.users WHERE auth_sub = $1`, authSub).Scan(&dbEnabled)
	if dbEnabled {
		t.Error("account.users.mfa_enabled: want false when no factor is verified")
	}
}

func testRedisAccount(t *testing.T) *goredis.Client {
	t.Helper()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	c, err := cache.NewClient(t.Context(), config.RedisConfig{URL: url})
	if err != nil {
		t.Fatalf("redis client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestIntegration_RecordLoginEvent_SyncsStaleMFAStatus regression-tests H2's
// login-time refresh: a caller whose account.users.mfa_enabled is stale
// (false, because their client never called POST /me/mfa/sync after they
// enrolled a factor elsewhere) gets it corrected the next time
// RecordLoginEvent fires — the hook the auth middleware calls on every
// authenticated request, gated to once per 30 minutes per user — instead
// of staying wrong until the client happens to sync.
func TestIntegration_RecordLoginEvent_SyncsStaleMFAStatus(t *testing.T) {
	const authSub = "integ_profile_mfa_login_sync"
	pool := testPool(t)
	redis := testRedisAccount(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, authSub)
		redis.Del(context.Background(), "account_test:login_gate:"+authSub)
	})

	admin := fakeSupabaseAdmin(`[{"status":"verified"}]`)
	defer admin.Close()

	mod := account.NewModuleForTestWithAdminAndRedis(pool, admin.URL, "test-service-role-key", redis)

	// seed a profile with the stale mfa_enabled=false the sync must correct
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO account.users (auth_sub, email, mfa_enabled) VALUES ($1, $2, false)`,
		authSub, "mfa-login-sync@test.com"); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	mod.RecordLoginEvent(t.Context(), authSub, "127.0.0.1", "test-agent")

	deadline := time.Now().Add(2 * time.Second)
	var dbEnabled bool
	for time.Now().Before(deadline) {
		pool.QueryRow(t.Context(), `SELECT mfa_enabled FROM account.users WHERE auth_sub = $1`, authSub).Scan(&dbEnabled)
		if dbEnabled {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !dbEnabled {
		t.Error("account.users.mfa_enabled: want true after RecordLoginEvent synced a verified factor, still false")
	}
}

// TestIntegration_RecordLoginEvent_FailsClosedOnRedisError guards against a
// regression where the rate-gate ignored Redis errors (`exists, _ :=
// ...Exists(...)`) and treated an unreadable gate as "not gated yet" — on
// every authenticated request. A Redis outage would then turn into a login
// event insert plus a Supabase Admin API call per request instead of being
// skipped. Points the client at an address nothing listens on so
// Exists/Set fail the same way a real outage would, with no real Redis
// needed.
func TestIntegration_RecordLoginEvent_FailsClosedOnRedisError(t *testing.T) {
	const authSub = "integ_login_gate_fail_closed"
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.login_events WHERE auth_sub = $1`, authSub)
	})

	unreachable := goredis.NewClient(&goredis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 200 * time.Millisecond,
	})
	defer unreachable.Close()

	mod := account.NewModuleForTestWithAdminAndRedis(pool, "", "", unreachable)
	mod.RecordLoginEvent(t.Context(), authSub, "127.0.0.1", "test-agent")

	var count int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM account.login_events WHERE auth_sub = $1`, authSub,
	).Scan(&count); err != nil {
		t.Fatalf("count login_events: %v", err)
	}
	if count != 0 {
		t.Errorf("want 0 login_events rows when the rate-gate check errors, got %d", count)
	}
}

// stubExportOrgReader satisfies contracts.OrganizationReader with a single
// known membership — enough to exercise buildExportData's organizations
// section without a cross-module import into the organization package.
type stubExportOrgReader struct{ err error }

func (stubExportOrgReader) GetOrganizationByID(context.Context, string) (*contracts.OrganizationInfo, error) {
	return nil, errors.New("not implemented")
}
func (stubExportOrgReader) IsMember(context.Context, string, string) (bool, error) { return false, nil }
func (stubExportOrgReader) GetMemberRole(context.Context, string, string) (string, error) {
	return "", nil
}
func (stubExportOrgReader) ListMemberAuthSubs(context.Context, string) ([]string, error) {
	return nil, nil
}
func (stubExportOrgReader) GetFirstOrganizationIDForMember(context.Context, string) (string, error) {
	return "", nil
}
func (s stubExportOrgReader) ListMembershipsForExport(_ context.Context, _ string) ([]contracts.OrgMembershipInfo, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []contracts.OrgMembershipInfo{{ID: "org_1", Slug: "acme", Name: "Acme", Role: "owner", JoinedAt: time.Now()}}, nil
}
func (stubExportOrgReader) ListOwnedOrganizationIDs(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (stubExportOrgReader) CountActiveOwnedOrganizations(_ context.Context, _ string) (int, error) {
	return 0, nil
}

// stubExportNotifReader satisfies contracts.NotificationReader with a
// single known message.
type stubExportNotifReader struct{ err error }

func (s stubExportNotifReader) ListForUser(_ context.Context, _ string, _ int) ([]contracts.MessageInfo, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []contracts.MessageInfo{{ID: "msg_1", Kind: "in_app", Channel: "test", Subject: "hi"}}, nil
}

func TestIntegration_ExportData_Worker_CompletesTask(t *testing.T) {
	const authSub = "integ_profile_export_worker"
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.tasks WHERE auth_sub = $1`, authSub)
		pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, authSub)
	})

	e := account.NewModuleEngineWithEmail(pool, authSub, "export-worker@test.com")
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, "/api/me", `{}`))

	wExp := httptest.NewRecorder()
	e.ServeHTTP(wExp, httpserver.JSONTestRequest(http.MethodPost, "/api/me/export", ""))
	if wExp.Code != http.StatusAccepted {
		t.Fatalf("request export: want 202, got %d: %s", wExp.Code, wExp.Body)
	}

	var resp map[string]any
	json.NewDecoder(wExp.Body).Decode(&resp)
	taskID := resp["data"].(map[string]any)["id"].(string)

	mod := account.NewModuleForTest(pool)
	mod.SetOrganizationReader(stubExportOrgReader{})
	mod.SetNotificationReader(stubExportNotifReader{})
	if err := mod.Worker.HandleExportData(t.Context(), encodeUserTaskEvent(events.RoutingKeyUserExportRequest, taskID, authSub)); err != nil {
		t.Fatalf("HandleExportData: %v", err)
	}

	var status string
	var result []byte
	pool.QueryRow(t.Context(), `SELECT status, result FROM account.tasks WHERE id = $1`, taskID).Scan(&status, &result)
	if status != "completed" {
		t.Errorf("task status: want completed, got %q", status)
	}

	var parsed struct {
		Profile       map[string]any   `json:"profile"`
		Organizations []map[string]any `json:"organizations"`
		Notifications []map[string]any `json:"notifications"`
		AuditEvents   []map[string]any `json:"audit_events"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("unmarshal task result: %v", err)
	}
	if parsed.Profile["email"] != "export-worker@test.com" {
		t.Errorf("profile.email: want export-worker@test.com, got %v", parsed.Profile["email"])
	}
	if len(parsed.Organizations) != 1 || parsed.Organizations[0]["slug"] != "acme" {
		t.Errorf("organizations: want [{slug: acme}], got %v", parsed.Organizations)
	}
	if len(parsed.Notifications) != 1 || parsed.Notifications[0]["subject"] != "hi" {
		t.Errorf("notifications: want [{subject: hi}], got %v", parsed.Notifications)
	}
	if parsed.AuditEvents == nil {
		t.Error("audit_events: want non-nil (possibly empty) slice")
	}
}

func TestIntegration_ExportData_Worker_SectionFailure_FailsWholeExport(t *testing.T) {
	const authSub = "integ_profile_export_worker_fail"
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.tasks WHERE auth_sub = $1`, authSub)
		pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, authSub)
	})

	e := account.NewModuleEngine(pool, authSub)
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, "/api/me", `{"email":"export-worker-fail@test.com"}`))

	wExp := httptest.NewRecorder()
	e.ServeHTTP(wExp, httpserver.JSONTestRequest(http.MethodPost, "/api/me/export", ""))
	if wExp.Code != http.StatusAccepted {
		t.Fatalf("request export: want 202, got %d: %s", wExp.Code, wExp.Body)
	}

	var resp map[string]any
	json.NewDecoder(wExp.Body).Decode(&resp)
	taskID := resp["data"].(map[string]any)["id"].(string)

	mod := account.NewModuleForTest(pool)
	mod.SetOrganizationReader(stubExportOrgReader{})
	mod.SetNotificationReader(stubExportNotifReader{err: errors.New("notification lookup boom")})
	if err := mod.Worker.HandleExportData(t.Context(), encodeUserTaskEvent(events.RoutingKeyUserExportRequest, taskID, authSub)); err == nil {
		t.Fatal("HandleExportData: want error when a section fails")
	}

	var status, taskErr string
	pool.QueryRow(t.Context(), `SELECT status, error FROM account.tasks WHERE id = $1`, taskID).Scan(&status, &taskErr)
	if status != "failed" {
		t.Errorf("task status: want failed (whole export fails on any section error), got %q", status)
	}
	if taskErr == "" {
		t.Error("task error: want a non-empty error message explaining which section failed")
	}
}

func TestIntegration_AuditLog_ListsOwnEventsAcrossOrganizations_NewestFirst(t *testing.T) {
	const authSub = "integ_profile_audit_log"
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM audit.events WHERE actor = $1`, authSub)
	})

	insert := func(organizationID *string, resource string, offset time.Duration) {
		_, err := pool.Exec(t.Context(), `
			INSERT INTO audit.events (organization_id, actor, action, resource, status_code, created_at)
			VALUES ($1, $2, 'POST', $3, 200, now() - $4::interval)`,
			organizationID, authSub, resource, fmt.Sprintf("%d seconds", int(offset.Seconds())),
		)
		if err != nil {
			t.Fatalf("seed audit event: %v", err)
		}
	}

	orgID := "ws-audit-test"
	insert(&orgID, "/api/v1/organizations/:id/members", 2*time.Second) // oldest
	insert(nil, "/api/v1/me", time.Second)                             // middle, no organization
	insert(&orgID, "/api/v1/organizations/:id/settings", 0)            // newest

	e := account.NewModuleEngine(pool, authSub)

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/api/me/audit-log?limit=2", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("audit log: want 200, got %d: %s", w.Code, w.Body)
	}

	var page1 struct {
		Data []struct {
			Resource string `json:"resource"`
		} `json:"data"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.NewDecoder(w.Body).Decode(&page1); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page1.Data) != 2 {
		t.Fatalf("page1: want 2 events, got %d", len(page1.Data))
	}
	if page1.Data[0].Resource != "/api/v1/organizations/:id/settings" || page1.Data[1].Resource != "/api/v1/me" {
		t.Errorf("page1 order: want [settings, me] newest first, got %+v", page1.Data)
	}
	if page1.NextCursor == "" {
		t.Fatal("page1: want non-empty next_cursor since a third event remains")
	}

	w2 := httptest.NewRecorder()
	e.ServeHTTP(w2, httpserver.JSONTestRequest(http.MethodGet,
		"/api/me/audit-log?limit=2&cursor="+url.QueryEscape(page1.NextCursor), ""))
	if w2.Code != http.StatusOK {
		t.Fatalf("audit log page 2: want 200, got %d: %s", w2.Code, w2.Body)
	}

	var page2 struct {
		Data []struct {
			Resource string `json:"resource"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w2.Body).Decode(&page2); err != nil {
		t.Fatalf("decode page2: %v", err)
	}
	if len(page2.Data) != 1 || page2.Data[0].Resource != "/api/v1/organizations/:id/members" {
		t.Errorf("page2: want [members], got %+v", page2.Data)
	}
}

func TestIntegration_ExportAuditLog_ReturnsCSV(t *testing.T) {
	const authSub = "integ_profile_audit_export"
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM audit.events WHERE actor = $1`, authSub)
	})

	orgID := "ws-audit-export-test"
	_, err := pool.Exec(t.Context(), `
		INSERT INTO audit.events (organization_id, actor, action, resource, status_code)
		VALUES ($1, $2, 'DELETE', '/api/v1/organizations/:id', 204)`,
		orgID, authSub,
	)
	if err != nil {
		t.Fatalf("seed audit event: %v", err)
	}

	e := account.NewModuleEngine(pool, authSub)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/api/me/audit-log/export", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("export: want 200, got %d: %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("content-type: want text/csv, got %q", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "id,organization_id,action,resource,status_code,ip,user_agent,created_at") {
		t.Errorf("csv header: want no actor column, got %q", body)
	}
	if !strings.Contains(body, orgID) || !strings.Contains(body, "/api/v1/organizations/:id") {
		t.Errorf("csv body: want seeded event, got %q", body)
	}
}

func TestIntegration_SupabaseUserUpdated_SyncsEmail(t *testing.T) {
	const authSub = "integ_profile_email_sync"
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, authSub)
		pool.Exec(context.Background(), `DELETE FROM audit.events WHERE actor = $1`, authSub)
	})

	// onboard first, mirroring what onAuthStateChange -> POST /me already does
	e := account.NewModuleEngineWithEmail(pool, authSub, "old@example.com")
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, "/api/me", `{}`))

	webhook := account.NewWebhookModuleEngine(pool, "Test1234")
	payload := `{"table":"users","record":{"id":"` + authSub + `","email":"new@example.com"},"old_record":{"email":"old@example.com"}}`
	req := httpserver.JSONTestRequest(http.MethodPost, "/webhooks/supabase/user-updated", payload)
	req.Header.Set("X-Webhook-Secret", "Test1234")
	w := httptest.NewRecorder()
	webhook.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("webhook: want 200, got %d: %s", w.Code, w.Body)
	}

	var email string
	pool.QueryRow(t.Context(), `SELECT email FROM account.users WHERE auth_sub = $1`, authSub).Scan(&email)
	if email != "new@example.com" {
		t.Errorf("account.users.email: want new@example.com, got %q", email)
	}

	var action, resource string
	var metadata []byte
	err := pool.QueryRow(t.Context(), `
		SELECT action, resource, metadata FROM audit.events WHERE actor = $1`, authSub,
	).Scan(&action, &resource, &metadata)
	if err != nil {
		t.Fatalf("audit event: want a row for %s, query failed: %v", authSub, err)
	}
	if action != "EMAIL_CHANGE" {
		t.Errorf("audit action: want EMAIL_CHANGE, got %q", action)
	}
	if resource != "/webhooks/supabase/user-updated" {
		t.Errorf("audit resource: want /webhooks/supabase/user-updated, got %q", resource)
	}
	if !strings.Contains(string(metadata), "old@example.com") || !strings.Contains(string(metadata), "new@example.com") {
		t.Errorf("audit metadata: want before/after emails, got %s", metadata)
	}
}

func TestIntegration_SupabaseUserUpdated_UnknownAuthSub_NoOp(t *testing.T) {
	pool := testPool(t)
	webhook := account.NewWebhookModuleEngine(pool, "Test1234")
	payload := `{"table":"users","record":{"id":"does_not_exist","email":"new@example.com"},"old_record":{"email":"old@example.com"}}`
	req := httpserver.JSONTestRequest(http.MethodPost, "/webhooks/supabase/user-updated", payload)
	req.Header.Set("X-Webhook-Secret", "Test1234")
	w := httptest.NewRecorder()
	webhook.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("webhook for a user with no account.users row: want 200 no-op, got %d: %s", w.Code, w.Body)
	}
}

func TestIntegration_ListSessions_CursorPagination(t *testing.T) {
	const authSub = "integ_profile_sessions_cursor"
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.login_events WHERE auth_sub = $1`, authSub)
		pool.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, authSub)
	})

	e := account.NewModuleEngine(pool, authSub)
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, "/api/me", `{"email":"sessions-cursor@test.com"}`))

	base := time.Now().Add(-time.Hour)
	for i := range 5 {
		if _, err := pool.Exec(t.Context(),
			`INSERT INTO account.login_events (auth_sub, ip_address, user_agent, created_at) VALUES ($1, $2, 'test-agent', $3)`,
			authSub, fmt.Sprintf("10.0.0.%d", i), base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("seed login event %d: %v", i, err)
		}
	}

	w1 := httptest.NewRecorder()
	e.ServeHTTP(w1, httpserver.JSONTestRequest(http.MethodGet, "/api/me/sessions?limit=3", ""))
	if w1.Code != http.StatusOK {
		t.Fatalf("page 1: want 200, got %d: %s", w1.Code, w1.Body)
	}
	var resp1 map[string]any
	json.NewDecoder(w1.Body).Decode(&resp1)
	page1, _ := resp1["data"].([]any)
	if len(page1) != 3 {
		t.Fatalf("page 1: want 3 sessions, got %d", len(page1))
	}
	nextCursor, _ := resp1["next_cursor"].(string)
	if nextCursor == "" {
		t.Fatal("page 1: want a non-empty next_cursor (5 seeded, limit 3)")
	}
	if _, ok := resp1["pagination"]; ok {
		t.Error("cursor mode: want no pagination object in the response")
	}

	w2 := httptest.NewRecorder()
	e.ServeHTTP(w2, httpserver.JSONTestRequest(http.MethodGet, "/api/me/sessions?limit=3&cursor="+url.QueryEscape(nextCursor), ""))
	if w2.Code != http.StatusOK {
		t.Fatalf("page 2: want 200, got %d: %s", w2.Code, w2.Body)
	}
	var resp2 map[string]any
	json.NewDecoder(w2.Body).Decode(&resp2)
	page2, _ := resp2["data"].([]any)
	if len(page2) != 2 {
		t.Fatalf("page 2: want 2 remaining sessions, got %d", len(page2))
	}

	seen := map[string]bool{}
	for _, row := range append(append([]any{}, page1...), page2...) {
		id, _ := row.(map[string]any)["id"].(string)
		if seen[id] {
			t.Errorf("session %s appeared on both pages", id)
		}
		seen[id] = true
	}
	if len(seen) != 5 {
		t.Errorf("want 5 distinct sessions across both pages, got %d", len(seen))
	}
}
