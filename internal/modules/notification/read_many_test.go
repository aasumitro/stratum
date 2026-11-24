package notification_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aasumitro/stratum/internal/modules/notification"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// One request marks the caller's listed notifications read, never another
// user's, and refuses ids that are not UUIDs.
func TestIntegration_MarkManyRead(t *testing.T) {
	const me, other = "integ_read_many_me", "integ_read_many_other"
	const orgID = "0e1e0000-0000-0000-0000-0000000000c7"
	pool := testPoolNotif(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM notification.messages WHERE organization_id = $1`, orgID)
	})
	seed := func(sub string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(t.Context(), `
			INSERT INTO notification.messages (organization_id, auth_sub, kind, channel, body, status)
			VALUES ($1, $2, 'in_app', 'test', 'x', 'sent') RETURNING id`, orgID, sub).Scan(&id); err != nil {
			t.Fatalf("seed: %v", err)
		}
		return id
	}
	a, b, c, theirs := seed(me), seed(me), seed(me), seed(other)
	e := notification.NewModuleEngine(pool, me)

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/api/me/notifications/read",
		fmt.Sprintf(`{"ids":[%q,%q,%q]}`, a, b, theirs)))
	if w.Code != http.StatusNoContent {
		t.Fatalf("mark many: want 204, got %d %s", w.Code, w.Body)
	}
	read := func(id string) bool {
		var isRead bool
		pool.QueryRow(t.Context(), `SELECT read_at IS NOT NULL FROM notification.messages WHERE id = $1`, id).Scan(&isRead)
		return isRead
	}
	if !read(a) || !read(b) || read(c) || read(theirs) {
		t.Errorf("read: a %v b %v c %v theirs %v; want true true false false", read(a), read(b), read(c), read(theirs))
	}

	w = httptest.NewRecorder()
	e.ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/api/me/notifications/read", `{"ids":["not-a-uuid"]}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad id: want 422, got %d", w.Code)
	}
}
