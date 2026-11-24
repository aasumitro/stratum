package account_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/account"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

var legalDocs = []account.LegalDocument{
	{Name: "terms", Version: "2026-11-01", URL: "https://example.com/terms"},
	{Name: "privacy", Version: "2026-10-01", URL: "https://example.com/privacy"},
}

type consentItem struct {
	Document        string  `json:"document"`
	Version         string  `json:"version"`
	URL             string  `json:"url"`
	AcceptedVersion *string `json:"accepted_version"`
}

func readConsents(t *testing.T, e http.Handler) []consentItem {
	t.Helper()
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/api/me/consents", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /me/consents: want 200, got %d %s", w.Code, w.Body)
	}
	var resp struct {
		Data []consentItem `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	return resp.Data
}

func postConsent(e http.Handler, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httpserver.JSONTestRequest(http.MethodPost, "/api/me/consents", body)
	req.Header.Set("User-Agent", "consent-test")
	e.ServeHTTP(w, req)
	return w
}

// Nothing is required while no version is configured; once it is, only the
// current version of a required document can be accepted, before the profile
// exists, and the latest acceptance is reported.
func TestIntegration_Consents(t *testing.T) {
	const authSub = "integ_consents_user"
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.consents WHERE auth_sub = $1`, authSub)
	})

	if got := readConsents(t, account.NewModuleEngineWithLegal(pool, authSub, nil)); len(got) != 0 {
		t.Fatalf("no documents configured: want [], got %v", got)
	}

	e := account.NewModuleEngineWithLegal(pool, authSub, legalDocs)
	got := readConsents(t, e)
	if len(got) != 2 || got[0].Document != "terms" || got[0].URL != "https://example.com/terms" || got[0].AcceptedVersion != nil {
		t.Fatalf("before accepting: got %+v", got)
	}

	if w := postConsent(e, `{"document":"terms","version":"2026-01-01"}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "CONSENT_VERSION_OUTDATED") {
		t.Errorf("old version: want 409 CONSENT_VERSION_OUTDATED, got %d %s", w.Code, w.Body)
	}
	if w := postConsent(e, `{"document":"cookies","version":"1"}`); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "CONSENT_DOCUMENT_UNKNOWN") {
		t.Errorf("unknown document: want 422 CONSENT_DOCUMENT_UNKNOWN, got %d %s", w.Code, w.Body)
	}
	if w := postConsent(e, `{"document":"terms","version":"2026-11-01"}`); w.Code != http.StatusNoContent {
		t.Fatalf("accept terms: want 204, got %d %s", w.Code, w.Body)
	}

	var ip, userAgent string
	pool.QueryRow(t.Context(), `SELECT ip, user_agent FROM account.consents WHERE auth_sub = $1`, authSub).Scan(&ip, &userAgent)
	if ip == "" || userAgent == "" {
		t.Errorf("acceptance must record where it came from: ip %q user agent %q", ip, userAgent)
	}

	got = readConsents(t, e)
	if got[0].AcceptedVersion == nil || *got[0].AcceptedVersion != "2026-11-01" {
		t.Errorf("terms: want accepted 2026-11-01, got %+v", got[0])
	}
	if got[1].AcceptedVersion != nil {
		t.Errorf("privacy: want not accepted, got %+v", got[1])
	}
}

// The runtime API role can record an acceptance but never change or remove one.
func TestIntegration_Consents_AppendOnlyForTheAPIRole(t *testing.T) {
	dsn := os.Getenv("POSTGRES_APP_URL")
	if dsn == "" {
		t.Skip("POSTGRES_APP_URL not set")
	}
	app, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	defer app.Close()
	const authSub = "integ_consents_append_only"
	pool := testPool(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM account.consents WHERE auth_sub = $1`, authSub)
	})

	if _, err := app.Exec(t.Context(), `INSERT INTO account.consents (auth_sub, document, version) VALUES ($1, 'terms', 'v1')`, authSub); err != nil {
		t.Fatalf("insert as stratum_app: %v", err)
	}
	if _, err := app.Exec(t.Context(), `UPDATE account.consents SET version = 'v2' WHERE auth_sub = $1`, authSub); err == nil {
		t.Error("update as stratum_app: want permission denied")
	}
	if _, err := app.Exec(t.Context(), `DELETE FROM account.consents WHERE auth_sub = $1`, authSub); err == nil {
		t.Error("delete as stratum_app: want permission denied")
	}
}

// The export carries every acceptance, and deleting the account removes them.
func TestIntegration_Consents_ExportedAndDeletedWithTheAccount(t *testing.T) {
	const authSub = "integ_consents_gdpr"
	pool := testPool(t)
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM account.consents WHERE auth_sub = $1`, authSub)
		pool.Exec(ctx, `DELETE FROM account.tasks WHERE auth_sub = $1`, authSub)
		pool.Exec(ctx, `DELETE FROM account.users WHERE auth_sub = $1`, authSub)
		pool.Exec(ctx, `DELETE FROM messaging.outbox WHERE payload->'data'->>'auth_sub' = $1`, authSub)
	})
	e := account.NewModuleEngineWithLegal(pool, authSub, legalDocs)
	e.ServeHTTP(httptest.NewRecorder(), httpserver.JSONTestRequest(http.MethodPost, "/api/me", `{}`))
	if w := postConsent(e, `{"document":"privacy","version":"2026-10-01"}`); w.Code != http.StatusNoContent {
		t.Fatalf("accept privacy: want 204, got %d %s", w.Code, w.Body)
	}

	var exportTask, deleteTask string
	pool.QueryRow(t.Context(), `INSERT INTO account.tasks (auth_sub, kind) VALUES ($1, 'export_data') RETURNING id`, authSub).Scan(&exportTask)
	mod := account.NewModuleForTest(pool)
	mod.SetOrganizationReader(stubExportOrgReader{})
	mod.SetNotificationReader(stubExportNotifReader{})
	if err := mod.Worker.HandleExportData(t.Context(), encodeUserTaskEvent(events.RoutingKeyUserExportRequest, exportTask, authSub)); err != nil {
		t.Fatalf("HandleExportData: %v", err)
	}
	var result []byte
	pool.QueryRow(t.Context(), `SELECT result FROM account.tasks WHERE id = $1`, exportTask).Scan(&result)
	var parsed struct {
		Consents []map[string]any `json:"consents"`
	}
	json.Unmarshal(result, &parsed)
	if len(parsed.Consents) != 1 || parsed.Consents[0]["document"] != "privacy" {
		t.Errorf("export consents: got %v", parsed.Consents)
	}

	pool.QueryRow(t.Context(), `INSERT INTO account.tasks (auth_sub, kind) VALUES ($1, 'delete_account') RETURNING id`, authSub).Scan(&deleteTask)
	mod.SetOrganizationReader(stubOwnerOrgReader{owned: 0})
	mod.SetOrganizationWriter(okWriter{})
	mod.SetNotificationWriter(okWriter{})
	mod.SetBillingWriter(okWriter{})
	if err := mod.Worker.HandleDeleteAccount(t.Context(), encodeUserTaskEvent(events.RoutingKeyUserDeleteRequest, deleteTask, authSub)); err != nil {
		t.Fatalf("HandleDeleteAccount: %v", err)
	}
	var left int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM account.consents WHERE auth_sub = $1`, authSub).Scan(&left)
	if left != 0 {
		t.Errorf("consents after deletion: want 0, got %d", left)
	}
}
