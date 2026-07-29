package events_test

import (
	"testing"

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
