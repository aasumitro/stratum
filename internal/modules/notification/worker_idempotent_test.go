package notification_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/notification"
)

func cleanupNotifProcessedEvent(pool *pgxpool.Pool, eventID string) {
	pool.Exec(context.Background(),
		`DELETE FROM notification.processed_events WHERE event_id = $1`, eventID)
}

// TestIdempotent_MalformedBody_StillCallsNext needs no DB: a body without a
// readable envelope ID must fail open (proceed) rather than block delivery
// over a decode hiccup — mirrors this codebase's other optional/degraded
// signal fail-open conventions.
func TestIdempotent_MalformedBody_StillCallsNext(t *testing.T) {
	mod := notification.New(nil, nil, nil, nil, "", nil)
	called := false
	next := func(_ context.Context, _ []byte) error {
		called = true
		return nil
	}

	if err := mod.Worker.Idempotent(next)(t.Context(), []byte("not json")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected next to be called when the envelope ID can't be read")
	}
}

// TestIntegration_Idempotent_SkipsRedeliveredEvent confirms the same
// envelope ID delivered twice (RabbitMQ's at-least-once guarantee) only
// produces one notification, not two.
func TestIntegration_Idempotent_SkipsRedeliveredEvent(t *testing.T) {
	pool := testPoolNotif(t)
	const orgID = "00000000-0000-0000-0000-000000000f07"
	// A real envelope ID is always a UUID (events.Enqueue uses uuid.NewV7()) —
	// notification.processed_events.event_id is typed uuid, so this must be
	// one too, unlike the short "trial-01"-style IDs elsewhere in this file
	// that never mattered before nothing consumed the ID.
	const eventID = "00000000-0000-0000-0000-0000000ee0f7"
	t.Cleanup(func() {
		cleanupNotifByOrganization(pool, orgID)
		cleanupNotifProcessedEvent(pool, eventID)
	})

	mod := notification.New(pool, nil, markReadOwnerReader{}, nil, "", nil)
	body := encodeEnvNotif(eventID, events.RoutingKeyTrialStarted, orgID,
		events.TrialStarted{OrgID: orgID, Plan: "growth", TrialEnd: time.Now().Add(14 * 24 * time.Hour)})

	handler := mod.Worker.Idempotent(mod.Worker.HandleTrialStarted)

	if err := handler(t.Context(), body); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if err := handler(t.Context(), body); err != nil {
		t.Fatalf("redelivery must not error: %v", err)
	}

	var count int
	pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM notification.messages WHERE organization_id = $1 AND channel = 'trial_started'`, orgID,
	).Scan(&count)
	if count != 1 {
		t.Errorf("want exactly one message across both deliveries, got %d", count)
	}
}

// TestIntegration_Idempotent_ClaimEventErrorPropagates guards that a real
// DB error (not just "already claimed") still surfaces as an error rather
// than being swallowed as a silent skip.
func TestIntegration_Idempotent_ClaimEventErrorPropagates(t *testing.T) {
	pool := testPoolNotif(t)
	mod := notification.New(pool, nil, nil, nil, "", nil)

	// An event ID that isn't valid UUID text makes the INSERT itself fail
	// (the column is typed uuid), exercising claimEvent's error path.
	body := []byte(`{"id":"not-a-uuid","type":"x","source":"test","data":{}}`)
	err := mod.Worker.Idempotent(func(_ context.Context, _ []byte) error { return nil })(t.Context(), body)
	if err == nil {
		t.Fatal("expected an error for an invalid event_id, got nil")
	}
}
