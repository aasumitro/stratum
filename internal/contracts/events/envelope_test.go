package events_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts/events"
)

func TestEnvelopeID_ReadsIDWithoutDecodingData(t *testing.T) {
	body := []byte(`{"id":"evt-123","type":"org.created","source":"organization","data":{"anything":"goes here"}}`)

	id, err := events.EnvelopeID(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "evt-123" {
		t.Errorf("EnvelopeID = %q, want %q", id, "evt-123")
	}
}

func TestEnvelopeID_MalformedBody_Errors(t *testing.T) {
	if _, err := events.EnvelopeID([]byte("not json")); err == nil {
		t.Fatal("expected an error for malformed JSON, got nil")
	}
}

func TestEnvelopeID_MissingID_ReturnsEmptyNoError(t *testing.T) {
	id, err := events.EnvelopeID([]byte(`{"type":"org.created"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "" {
		t.Errorf("EnvelopeID = %q, want empty", id)
	}
}

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

// TestIntegration_Enqueue_WritesWellFormedEnvelope inserts an outbox row via
// Enqueue inside a real transaction, then asserts the row's payload is a
// complete Envelope carrying the given inputs verbatim and a freshly
// generated uuidv7 ID — the relay ships this payload to the broker
// unchanged, so the outbox row has to be a faithful envelope on its own.
func TestIntegration_Enqueue_WritesWellFormedEnvelope(t *testing.T) {
	pool := testPool(t)
	data := map[string]string{"k": "v"}

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if err := events.Enqueue(t.Context(), tx, events.ExchangeOrganization,
		"org.created", "organization", "org-1", data); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var payload []byte
	err = tx.QueryRow(t.Context(),
		`SELECT payload FROM messaging.outbox WHERE routing_key = 'org.created' AND exchange = $1`,
		events.ExchangeOrganization,
	).Scan(&payload)
	if err != nil {
		t.Fatalf("query outbox row: %v", err)
	}

	var got struct {
		Type   string            `json:"type"`
		Source string            `json:"source"`
		OrgID  string            `json:"org_id"`
		Data   map[string]string `json:"data"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal outbox payload: %v", err)
	}
	want := struct {
		Type   string            `json:"type"`
		Source string            `json:"source"`
		OrgID  string            `json:"org_id"`
		Data   map[string]string `json:"data"`
	}{Type: "org.created", Source: "organization", OrgID: "org-1", Data: data}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("outbox envelope = %+v, want %+v", got, want)
	}

	id := decodeEnvelopeID(t, payload)
	if v := uuidVersion(id); v != 7 {
		t.Errorf("outbox row's envelope ID version = %d, want 7 (uuidv7)", v)
	}
}

// TestIntegration_EnqueueDelayed_SetsFutureNotBefore proves EnqueueDelayed
// folds delay into not_before rather than a separate mechanism — the
// relay's claimUnpublished query relies on this to unify "publish now" and
// "publish later" into one WHERE clause.
func TestIntegration_EnqueueDelayed_SetsFutureNotBefore(t *testing.T) {
	pool := testPool(t)

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if err := events.EnqueueDelayed(t.Context(), tx, events.ExchangeBilling,
		"billing.retry", "billing", "org-1", map[string]string{"k": "v"}, time.Hour); err != nil {
		t.Fatalf("EnqueueDelayed: %v", err)
	}

	var notBefore, createdAt time.Time
	var payload []byte
	err = tx.QueryRow(t.Context(),
		`SELECT not_before, created_at, payload FROM messaging.outbox WHERE routing_key = 'billing.retry'`,
	).Scan(&notBefore, &createdAt, &payload)
	if err != nil {
		t.Fatalf("query outbox row: %v", err)
	}
	if !notBefore.After(createdAt.Add(59 * time.Minute)) {
		t.Errorf("not_before = %v, want at least ~1h after created_at %v", notBefore, createdAt)
	}
	// EnqueueDelayed builds its Envelope independently of Enqueue — guard its
	// ID generation against a uuid.NewV7 → uuid.New (v4) regression too.
	if v := uuidVersion(decodeEnvelopeID(t, payload)); v != 7 {
		t.Errorf("outbox row's envelope ID version = %d, want 7 (uuidv7)", v)
	}
}

func decodeEnvelopeID(t *testing.T, body []byte) uuid.UUID {
	t.Helper()
	var env struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal published envelope: %v", err)
	}
	id, err := uuid.Parse(env.ID)
	if err != nil {
		t.Fatalf("parse envelope ID %q: %v", env.ID, err)
	}
	return id
}

// uuidVersion extracts the RFC 9562 version number — the high nibble of
// octet 6. stdlib uuid.UUID (unlike github.com/google/uuid) exposes no
// Version() accessor.
func uuidVersion(u uuid.UUID) int { return int(u[6] >> 4) }
