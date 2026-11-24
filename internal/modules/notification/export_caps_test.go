package notification_test

import (
	"context"
	"testing"

	"github.com/aasumitro/stratum/internal/modules/notification"
)

// The export caps emails and other messages apart, so many emails never crowd
// out a user's in-app history; the result stays newest first.
func TestIntegration_ListForUser_CapsEachKindApart(t *testing.T) {
	const authSub = "integ_export_caps_user"
	const orgID = "0e1e0000-0000-0000-0000-0000000000c8"
	pool := testPoolNotif(t)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM notification.messages WHERE organization_id = $1`, orgID)
	})
	for i, kind := range []string{"email", "email", "email", "in_app", "in_app", "in_app"} {
		if _, err := pool.Exec(t.Context(), `
			INSERT INTO notification.messages (organization_id, auth_sub, kind, channel, body, status, created_at)
			VALUES ($1, $2, $3, 'test', 'x', 'sent', now() - make_interval(mins => $4))`,
			orgID, authSub, kind, i); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	got, err := notification.NewModuleForTest(pool).ListForUser(t.Context(), authSub, 2)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	kinds := map[string]int{}
	for i, m := range got {
		kinds[m.Kind]++
		if i > 0 && m.CreatedAt.After(got[i-1].CreatedAt) {
			t.Errorf("not newest first at %d", i)
		}
	}
	if len(got) != 4 || kinds["email"] != 2 || kinds["in_app"] != 2 {
		t.Errorf("want 2 emails and 2 in-app messages, got %d rows %v", len(got), kinds)
	}
}
