package query_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/studio/internal/query"
)

// Reasons are counted per request within the window, and only requests with
// free text are listed, newest first.
func TestGetCancellationReasons(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	ctx := t.Context()

	var sub string
	if err := pool.QueryRow(ctx, `
		INSERT INTO billing.subscriptions (subject_type, subject_id, status)
		VALUES ('organization', '0e1e0000-0000-0000-0000-0000000000e8', 'active') RETURNING id`).Scan(&sub); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM billing.subscription_history WHERE subscription_id = $1`, sub)
		pool.Exec(context.Background(), `DELETE FROM billing.subscriptions WHERE id = $1`, sub)
	})
	for _, r := range []struct {
		metadata, age string
	}{
		{`{"reason":"test_reason_a","details":"budget cut"}`, "1 day"},
		{`{"reason":"test_reason_a"}`, "2 days"},
		{`{"reason":"test_reason_b","details":"needs SSO"}`, "3 days"},
		{`{"reason":"test_reason_a","details":"old"}`, "200 days"},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO billing.subscription_history (subscription_id, action, from_plan, currency, changed_by, changed_at, metadata)
			VALUES ($1, 'cancel', 'pro', 'USD', 'owner', now() - $2::interval, $3::jsonb)`, sub, r.age, r.metadata); err != nil {
			t.Fatalf("seed history: %v", err)
		}
	}

	rep, err := query.GetCancellationReasons(ctx, pool, 30)
	if err != nil {
		t.Fatalf("GetCancellationReasons: %v", err)
	}
	counts := map[string]int64{}
	for _, c := range rep.Counts {
		counts[c.Reason] = c.Count
	}
	// Reasons only this test writes, so other rows cannot change the counts;
	// the 200-day-old request is outside the window.
	if counts["test_reason_a"] != 2 || counts["test_reason_b"] != 1 {
		t.Errorf("counts: want test_reason_a 2, test_reason_b 1, got %v", counts)
	}
	var ours []string
	for _, l := range rep.Latest {
		if l.Details == "budget cut" || l.Details == "needs SSO" || l.Details == "old" {
			ours = append(ours, l.Details)
		}
	}
	if len(ours) != 2 || ours[0] != "budget cut" || ours[1] != "needs SSO" {
		t.Errorf("latest details: got %v", ours)
	}
}
