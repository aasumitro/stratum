package account

import "testing"

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
