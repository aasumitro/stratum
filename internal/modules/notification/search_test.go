package notification_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aasumitro/stratum/internal/modules/notification"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// The feed searches subject and body; a term under three characters is refused.
func TestIntegration_ListNotifications_Search(t *testing.T) {
	const authSub = "integ_notif_search_user"
	const orgID = "0e1e0000-0000-0000-0000-0000000000c3"
	pool := testPoolNotif(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM notification.messages WHERE auth_sub = $1`, authSub)
	})
	for _, m := range []struct{ subject, body string }{
		{"Invoice paid", "Invoice INV-4242 was paid."},
		{"Member joined", "Ana joined the organization."},
		{"Usage", "You used 50% of your seats."},
		{"Usage", "You used 50 of your seats."},
	} {
		if _, err := pool.Exec(t.Context(), `
			INSERT INTO notification.messages (organization_id, auth_sub, kind, channel, subject, body, status)
			VALUES ($1, $2, 'in_app', 'test', $3, $4, 'sent')`, orgID, authSub, m.subject, m.body); err != nil {
			t.Fatalf("seed message: %v", err)
		}
	}
	e := notification.NewModuleEngine(pool, authSub)

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/api/me/notifications?q=in", ""))
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "SEARCH_TOO_SHORT") {
		t.Fatalf("short term: want 422 SEARCH_TOO_SHORT, got %d %s", w.Code, w.Body)
	}

	w = httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/api/me/notifications?page=1&q=inv-4242", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("search: want 200, got %d %s", w.Code, w.Body)
	}
	var resp struct {
		Data []struct {
			Subject string `json:"subject"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Data) != 1 || resp.Data[0].Subject != "Invoice paid" {
		t.Errorf("search inv-4242: got %+v", resp.Data)
	}

	w = httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/api/me/notifications?page=1&q="+url.QueryEscape("50% of"), ""))
	resp.Data = nil
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Data) != 1 {
		t.Errorf("literal %% search: want 1, got %d", len(resp.Data))
	}
}
