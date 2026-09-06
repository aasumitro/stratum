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

// TestCriticalCleanupSteps_Classification pins which delete-account cleanup
// steps block completion on failure (personal data in another schema — the
// task must retry) versus which are best-effort (failure only recorded in
// failed_steps). Getting a step on the wrong side either lets an erasure
// report success with PII still live, or wedges deletion behind a
// non-essential step.
func TestCriticalCleanupSteps_Classification(t *testing.T) {
	critical := []string{"notifications", "memberships", "audit_log", "billing_history"}
	bestEffort := []string{"login_events", "avatar"}

	for _, name := range critical {
		if !criticalCleanupSteps[name] {
			t.Errorf("step %q: want classified critical, got best-effort", name)
		}
	}
	for _, name := range bestEffort {
		if criticalCleanupSteps[name] {
			t.Errorf("step %q: want classified best-effort, got critical", name)
		}
	}
	if len(criticalCleanupSteps) != len(critical) {
		t.Errorf("criticalCleanupSteps: want exactly %d entries %v, got %d (%v)",
			len(critical), critical, len(criticalCleanupSteps), criticalCleanupSteps)
	}
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
