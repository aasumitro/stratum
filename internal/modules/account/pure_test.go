package account

import (
	"testing"
	"time"
)

func TestVerifyWebhookSecret(t *testing.T) {
	t.Run("empty config secret bypasses check (dev mode)", func(t *testing.T) {
		if !verifyWebhookSecret("anything", "") {
			t.Error("empty config should return true")
		}
	})

	t.Run("matching secrets accepted", func(t *testing.T) {
		if !verifyWebhookSecret("Test1234", "Test1234") {
			t.Error("matching secrets should return true")
		}
	})

	t.Run("wrong header secret rejected", func(t *testing.T) {
		if verifyWebhookSecret("wrong", "Test1234") {
			t.Error("wrong secret should return false")
		}
	})

	t.Run("empty header with non-empty config rejected", func(t *testing.T) {
		if verifyWebhookSecret("", "Test1234") {
			t.Error("empty header should return false when config secret is set")
		}
	})
}

// TestRevokeAllSessions_NilRevokedNS_NoPanic guards against regressing to a
// nil-pointer panic on s.revokedNS.Set — revokedNS is a plain constructor
// arg (not a Set*-after-construction wire), already nil in the worker's own
// account.Module construction and in most test helpers, so a real caller
// can reach this with sessionID and exp both present.
func TestRevokeAllSessions_NilRevokedNS_NoPanic(t *testing.T) {
	s := &service{} // revokedNS deliberately left nil
	err := s.revokeAllSessions(t.Context(), "sub_1", "session_1", time.Now().Add(time.Hour))
	if err != nil {
		t.Errorf("want nil error with unwired revokedNS, got %v", err)
	}
}
