package organization

import (
	"encoding/json"
	"testing"
)

// A delivery with a stored body is resent byte for byte; one recorded before
// bodies were kept gets the rebuilt {id, type, endpoint_id} envelope.
func TestRetryPayload(t *testing.T) {
	stored := []byte(`{"type":"x",  "id":"e1"}`)
	got, err := retryPayload(&webhookDeliveryRecord{EventID: "e1", EventType: "x", Payload: stored}, "ep1")
	if err != nil || string(got) != string(stored) {
		t.Fatalf("stored body: want %s, got %s (%v)", stored, got, err)
	}

	got, err = retryPayload(&webhookDeliveryRecord{EventID: "e1", EventType: "x"}, "ep1")
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]string
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatal(err)
	}
	if env[eventPayloadIDKey] != "e1" || env[eventPayloadTypeKey] != "x" || env[eventPayloadEndpointIDKey] != "ep1" || len(env) != 3 {
		t.Errorf("legacy envelope: got %v", env)
	}
}
