package organization

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

var hexRE = regexp.MustCompile(`^[0-9a-f]*$`)

func TestGenerateToken(t *testing.T) {
	for _, length := range []int{0, 8, 16, 32} {
		got, err := generateToken(length)
		if err != nil {
			t.Fatalf("generateToken(%d): %v", length, err)
		}
		if len(got) != length*2 {
			t.Errorf("generateToken(%d): got len %d, want %d", length, len(got), length*2)
		}
		if !hexRE.MatchString(got) {
			t.Errorf("generateToken(%d): non-hex chars in %q", length, got)
		}
	}
	// uniqueness — two calls with same length produce different values
	a, _ := generateToken(16)
	b, _ := generateToken(16)
	if a == b {
		t.Error("generateToken: two calls returned identical tokens")
	}
}

func TestIPLocksOutCaller(t *testing.T) {
	cases := []struct {
		name       string
		callerIP   string
		allowedIPs []string
		want       bool
	}{
		{"empty allowlist means allow all", "203.0.113.5", nil, false},
		{"caller inside a CIDR block", "10.0.0.42", []string{"10.0.0.0/8"}, false},
		{"caller outside every CIDR block", "203.0.113.5", []string{"10.0.0.0/8", "192.168.1.0/24"}, true},
		{"caller matches a bare IP entry (no CIDR suffix)", "203.0.113.5", []string{"203.0.113.5"}, false},
		{"caller does not match any bare IP entry", "203.0.113.6", []string{"203.0.113.5"}, true},
		{"unparseable caller IP is treated as locked out", "not-an-ip", []string{"10.0.0.0/8"}, true},
		{"malformed CIDR entries are skipped, valid ones still apply", "10.0.0.42", []string{"garbage", "10.0.0.0/8"}, false},
		{"IPv6 CIDR match", "2001:db8::1", []string{"2001:db8::/32"}, false},
		{"IPv6 caller outside allowlist", "2001:db8::1", []string{"2001:db9::/32"}, true},
		// Regression: this check shares its per-entry matching with the
		// request-time allowlist gate (middleware.IPEntryMatches) — an
		// IPv4-mapped IPv6 caller must resolve the same way in both places,
		// or an owner could save a change this check calls safe and then
		// get locked out anyway.
		{"IPv4-mapped IPv6 caller matches bare IPv4 entry", "::ffff:203.0.113.5", []string{"203.0.113.5"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ipLocksOutCaller(c.callerIP, c.allowedIPs); got != c.want {
				t.Errorf("ipLocksOutCaller(%q, %v) = %v, want %v", c.callerIP, c.allowedIPs, got, c.want)
			}
		})
	}
}

func TestSignWebhookPayload(t *testing.T) {
	payload := []byte(`{"type":"test.ping"}`)
	at := time.Unix(1700000000, 0)

	sig := signWebhookPayload(payload, "secret-a", at)
	if !strings.HasPrefix(sig, "t=1700000000,v1=") {
		t.Errorf("signWebhookPayload: got %q, want t=1700000000,v1=... prefix", sig)
	}

	// deterministic for identical inputs
	if got := signWebhookPayload(payload, "secret-a", at); got != sig {
		t.Errorf("signWebhookPayload: not deterministic, got %q want %q", got, sig)
	}

	// a different secret must produce a different signature — this is the
	// entire point of the dual-signature rotation grace window: the
	// receiver tells old vs. new apart only by which one verifies.
	if got := signWebhookPayload(payload, "secret-b", at); got == sig {
		t.Error("signWebhookPayload: different secrets produced identical signatures")
	}

	// a different payload must also change the signature
	if got := signWebhookPayload([]byte(`{"type":"other"}`), "secret-a", at); got == sig {
		t.Error("signWebhookPayload: different payloads produced identical signatures")
	}
}

// TestValidateImportRows_ValidIdxMapsToOriginalRow regression-tests that
// validIdx lets a caller map a valid row back to its position in the
// originally submitted batch — the commit loop in importMembers indexes
// valid by position within the filtered subset, which drifts from the
// original row order as soon as an earlier row fails validation.
func TestValidateImportRows_ValidIdxMapsToOriginalRow(t *testing.T) {
	rows := []importRowRaw{
		{Email: "not-an-email", Role: "member"},  // index 0: fails validation
		{Email: "b@example.com", Role: "member"}, // index 1: valid
		{Email: "c@example.com", Role: "admin"},  // index 2: valid
	}

	valid, validIdx, errs := validateImportRows(rows)

	if len(errs) != 1 || errs[0].Index != 0 {
		t.Fatalf("errs = %+v, want one error at index 0", errs)
	}
	if len(valid) != 2 || len(validIdx) != 2 {
		t.Fatalf("valid = %+v, validIdx = %v, want 2 entries each", valid, validIdx)
	}
	if valid[0].Email != "b@example.com" || validIdx[0] != 1 {
		t.Errorf("valid[0] = %+v at original index %d, want b@example.com at index 1", valid[0], validIdx[0])
	}
	if valid[1].Email != "c@example.com" || validIdx[1] != 2 {
		t.Errorf("valid[1] = %+v at original index %d, want c@example.com at index 2", valid[1], validIdx[1])
	}
}

func TestAttemptLogEntry(t *testing.T) {
	status, latency := 200, 42
	at := time.Unix(1700000000, 0)

	raw := attemptLogEntry(1, &status, &latency, nil, at)
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("attemptLogEntry: not valid JSON array: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("attemptLogEntry: got %d entries, want 1", len(entries))
	}
	entry := entries[0]
	if entry["attempt"] != float64(1) {
		t.Errorf("attemptLogEntry: attempt = %v, want 1", entry["attempt"])
	}
	if entry["status_code"] != float64(200) {
		t.Errorf("attemptLogEntry: status_code = %v, want 200", entry["status_code"])
	}
	if _, hasError := entry["error"]; hasError {
		t.Error("attemptLogEntry: unexpected error field on a successful attempt")
	}

	errMsg := "connection refused"
	raw = attemptLogEntry(2, nil, nil, &errMsg, at)
	entries = nil
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("attemptLogEntry: not valid JSON array: %v", err)
	}
	if entries[0]["error"] != errMsg {
		t.Errorf("attemptLogEntry: error = %v, want %q", entries[0]["error"], errMsg)
	}
	if _, hasStatus := entries[0]["status_code"]; hasStatus {
		t.Error("attemptLogEntry: unexpected status_code field on a network-error attempt")
	}
}
