package config_test

import (
	"testing"
	"time"

	"github.com/aasumitro/stratum/internal/platform/config"
)

func TestRequireSecretsOutsideDev(t *testing.T) {
	base := func() config.Config {
		return config.Config{
			Env:        "production",
			StatsToken: "s",
			Stripe:     config.StripeConfig{WebhookSecret: "s"},
			Xendit:     config.XenditConfig{CallbackToken: "x", AllowedCIDRs: []string{"192.0.2.0/24"}},
			Auth:       config.AuthConfig{WebhookSecret: "a"},
		}
	}

	t.Run("development skips the check entirely", func(t *testing.T) {
		cfg := config.Config{Env: "development"}
		if err := cfg.RequireSecretsOutsideDev(); err != nil {
			t.Errorf("unexpected error in development: %v", err)
		}
	})

	t.Run("production with all secrets set passes", func(t *testing.T) {
		cfg := base()
		if err := cfg.RequireSecretsOutsideDev(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	for _, tc := range []struct {
		name   string
		mutate func(*config.Config)
	}{
		{"missing stripe secret", func(c *config.Config) { c.Stripe.WebhookSecret = "" }},
		{"missing xendit token", func(c *config.Config) { c.Xendit.CallbackToken = "" }},
		{"missing xendit cidrs", func(c *config.Config) { c.Xendit.AllowedCIDRs = nil }},
		{"missing supabase secret", func(c *config.Config) { c.Auth.WebhookSecret = "" }},
		{"missing stats token", func(c *config.Config) { c.StatsToken = "" }},
		{"webhook encryption key is still the .env.example placeholder", func(c *config.Config) {
			c.WebhookSecretEncryptionKey = "CHANGE_ME_generate_with_openssl_rand_base64_32"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.mutate(&cfg)
			if err := cfg.RequireSecretsOutsideDev(); err == nil {
				t.Error("expected an error, got nil")
			}
		})
	}
}

func TestRequireGeoIPDBOutsideDev(t *testing.T) {
	t.Run("development skips the check entirely", func(t *testing.T) {
		cfg := config.Config{Env: "development"}
		if err := cfg.RequireGeoIPDBOutsideDev(); err != nil {
			t.Errorf("unexpected error in development: %v", err)
		}
	})

	t.Run("production with GEOIP_DB_PATH set passes", func(t *testing.T) {
		cfg := config.Config{Env: "production", GeoIPDBPath: "/data/GeoLite2-Country.mmdb"}
		if err := cfg.RequireGeoIPDBOutsideDev(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("production with GEOIP_DB_PATH unset fails", func(t *testing.T) {
		cfg := config.Config{Env: "production"}
		if err := cfg.RequireGeoIPDBOutsideDev(); err == nil {
			t.Error("expected an error, got nil")
		}
	})
}

func TestLoad_WebhookSecretEncryptionKey(t *testing.T) {
	setOtherRequiredEnv := func(t *testing.T) {
		t.Helper()
		t.Setenv("POSTGRES_APP_URL", "postgres://u:p@localhost:5432/db")
		t.Setenv("POSTGRES_WORKER_URL", "postgres://u:p@localhost:5432/db")
		t.Setenv("POSTGRES_WEBHOOK_URL", "postgres://u:p@localhost:5432/db")
		t.Setenv("REDIS_URL", "redis://localhost:6379")
		t.Setenv("RABBITMQ_URL", "amqp://localhost:5672")
		t.Setenv("AUTH_JWKS_URL", "https://example.com/.well-known/jwks.json")
		t.Setenv("AUTH_ISSUER", "https://example.com")
	}

	t.Run("set to a real value loads successfully", func(t *testing.T) {
		setOtherRequiredEnv(t)
		t.Setenv("WEBHOOK_SECRET_ENCRYPTION_KEY", "a-real-key")
		if _, err := config.Load(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("set but empty fails to load", func(t *testing.T) {
		setOtherRequiredEnv(t)
		t.Setenv("WEBHOOK_SECRET_ENCRYPTION_KEY", "")
		if _, err := config.Load(); err == nil {
			t.Error("expected an error when WEBHOOK_SECRET_ENCRYPTION_KEY is set but empty, got nil")
		}
	})
}

func TestLoad_WebhookSecretEncryptionKeyRotationFields(t *testing.T) {
	setOtherRequiredEnv := func(t *testing.T) {
		t.Helper()
		t.Setenv("POSTGRES_APP_URL", "postgres://u:p@localhost:5432/db")
		t.Setenv("POSTGRES_WORKER_URL", "postgres://u:p@localhost:5432/db")
		t.Setenv("POSTGRES_WEBHOOK_URL", "postgres://u:p@localhost:5432/db")
		t.Setenv("REDIS_URL", "redis://localhost:6379")
		t.Setenv("RABBITMQ_URL", "amqp://localhost:5672")
		t.Setenv("AUTH_JWKS_URL", "https://example.com/.well-known/jwks.json")
		t.Setenv("AUTH_ISSUER", "https://example.com")
		t.Setenv("WEBHOOK_SECRET_ENCRYPTION_KEY", "a-real-key")
	}

	t.Run("default empty previous key and version 1", func(t *testing.T) {
		setOtherRequiredEnv(t)
		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.WebhookSecretEncryptionKeyPrevious != "" {
			t.Errorf("expected empty previous key, got %q", cfg.WebhookSecretEncryptionKeyPrevious)
		}
		if cfg.WebhookSecretEncryptionKeyVersion != 1 {
			t.Errorf("expected version 1, got %d", cfg.WebhookSecretEncryptionKeyVersion)
		}
	})

	t.Run("loads set values", func(t *testing.T) {
		setOtherRequiredEnv(t)
		t.Setenv("WEBHOOK_SECRET_ENCRYPTION_KEY_PREVIOUS", "old-key")
		t.Setenv("WEBHOOK_SECRET_ENCRYPTION_KEY_VERSION", "2")
		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.WebhookSecretEncryptionKeyPrevious != "old-key" {
			t.Errorf("expected 'old-key', got %q", cfg.WebhookSecretEncryptionKeyPrevious)
		}
		if cfg.WebhookSecretEncryptionKeyVersion != 2 {
			t.Errorf("expected version 2, got %d", cfg.WebhookSecretEncryptionKeyVersion)
		}
	})
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("POSTGRES_APP_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("POSTGRES_WORKER_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("POSTGRES_WEBHOOK_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("RABBITMQ_URL", "amqp://localhost:5672")
	t.Setenv("AUTH_JWKS_URL", "https://example.com/.well-known/jwks.json")
	t.Setenv("AUTH_ISSUER", "https://example.com")
	t.Setenv("WEBHOOK_SECRET_ENCRYPTION_KEY", "a-real-key")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.OutboxRetentionDays != 30 {
		t.Errorf("expected OutboxRetentionDays default to 30, got %d", cfg.OutboxRetentionDays)
	}
	if cfg.Auth.AccessTokenMaxTTL != time.Hour {
		t.Errorf("expected AccessTokenMaxTTL default to 1h, got %v", cfg.Auth.AccessTokenMaxTTL)
	}
}

func TestLoad_AuthAccessTokenMaxTTL(t *testing.T) {
	t.Setenv("POSTGRES_APP_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("POSTGRES_WORKER_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("POSTGRES_WEBHOOK_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("RABBITMQ_URL", "amqp://localhost:5672")
	t.Setenv("AUTH_JWKS_URL", "https://example.com/.well-known/jwks.json")
	t.Setenv("AUTH_ISSUER", "https://example.com")
	t.Setenv("WEBHOOK_SECRET_ENCRYPTION_KEY", "a-real-key")
	t.Setenv("AUTH_ACCESS_TOKEN_MAX_TTL", "2h")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Auth.AccessTokenMaxTTL != 2*time.Hour {
		t.Errorf("expected AccessTokenMaxTTL to be 2h, got %v", cfg.Auth.AccessTokenMaxTTL)
	}
}
