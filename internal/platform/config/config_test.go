package config_test

import (
	"bytes"
	"log/slog"
	"strings"
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
		t.Setenv("POSTGRES_UNSCOPED_URL", "postgres://u:p@localhost:5432/db")
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
		t.Setenv("POSTGRES_UNSCOPED_URL", "postgres://u:p@localhost:5432/db")
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
	t.Setenv("POSTGRES_UNSCOPED_URL", "postgres://u:p@localhost:5432/db")
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
	for name, tc := range map[string]struct{ got, want int }{
		"AuditRetentionDays":               {cfg.AuditRetentionDays, 400},
		"LoginEventRetentionDays":          {cfg.LoginEventRetentionDays, 400},
		"DataExportRetentionDays":          {cfg.DataExportRetentionDays, 7},
		"NotificationRetentionDays":        {cfg.NotificationRetentionDays, 180},
		"WebhookDeliveryRetentionDays":     {cfg.WebhookDeliveryRetentionDays, 30},
		"DeletedOrganizationRetentionDays": {cfg.DeletedOrganizationRetentionDays, 30},
		"WebhookEventRetentionDays":        {cfg.WebhookEventRetentionDays, 90},
	} {
		if tc.got != tc.want {
			t.Errorf("expected %s default to %d, got %d", name, tc.want, tc.got)
		}
	}
	if cfg.Auth.AccessTokenMaxTTL != time.Hour {
		t.Errorf("expected AccessTokenMaxTTL default to 1h, got %v", cfg.Auth.AccessTokenMaxTTL)
	}
}

func TestLoad_AuthAccessTokenMaxTTL(t *testing.T) {
	t.Setenv("POSTGRES_APP_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("POSTGRES_WORKER_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("POSTGRES_WEBHOOK_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("POSTGRES_UNSCOPED_URL", "postgres://u:p@localhost:5432/db")
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

func TestLoad_PostgresStatementTimeout(t *testing.T) {
	setRequiredEnv := func(t *testing.T) {
		t.Helper()
		t.Setenv("POSTGRES_APP_URL", "postgres://u:p@localhost:5432/db")
		t.Setenv("POSTGRES_WORKER_URL", "postgres://u:p@localhost:5432/db")
		t.Setenv("POSTGRES_WEBHOOK_URL", "postgres://u:p@localhost:5432/db")
		t.Setenv("POSTGRES_UNSCOPED_URL", "postgres://u:p@localhost:5432/db")
		t.Setenv("REDIS_URL", "redis://localhost:6379")
		t.Setenv("RABBITMQ_URL", "amqp://localhost:5672")
		t.Setenv("AUTH_JWKS_URL", "https://example.com/.well-known/jwks.json")
		t.Setenv("AUTH_ISSUER", "https://example.com")
		t.Setenv("WEBHOOK_SECRET_ENCRYPTION_KEY", "a-real-key")
	}

	t.Run("defaults to 30s", func(t *testing.T) {
		setRequiredEnv(t)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Postgres.StatementTimeout != "30s" {
			t.Errorf("expected StatementTimeout default to be %q, got %q", "30s", cfg.Postgres.StatementTimeout)
		}
	})

	t.Run("reads POSTGRES_STATEMENT_TIMEOUT", func(t *testing.T) {
		setRequiredEnv(t)
		t.Setenv("POSTGRES_STATEMENT_TIMEOUT", "0")

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Postgres.StatementTimeout != "0" {
			t.Errorf("expected StatementTimeout to be %q, got %q", "0", cfg.Postgres.StatementTimeout)
		}
	})
}

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("POSTGRES_APP_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("POSTGRES_WORKER_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("POSTGRES_WEBHOOK_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("POSTGRES_UNSCOPED_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("RABBITMQ_URL", "amqp://localhost:5672")
	t.Setenv("AUTH_JWKS_URL", "https://example.com/.well-known/jwks.json")
	t.Setenv("AUTH_ISSUER", "https://example.com")
	t.Setenv("WEBHOOK_SECRET_ENCRYPTION_KEY", "a-real-key")
}

func TestLoad_AuditRetentionFloor(t *testing.T) {
	for value, wantErr := range map[string]bool{"30": true, "364": true, "365": false, "400": false} {
		t.Run(value, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("AUDIT_RETENTION_DAYS", value)
			_, err := config.Load()
			if wantErr && err == nil {
				t.Error("expected an error, got nil")
			}
			if !wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoad_RetentionMustBePositive(t *testing.T) {
	for _, env := range []string{
		"LOGIN_EVENT_RETENTION_DAYS",
		"DATA_EXPORT_RETENTION_DAYS",
		"NOTIFICATION_RETENTION_DAYS",
		"WEBHOOK_DELIVERY_RETENTION_DAYS",
		"DELETED_ORGANIZATION_RETENTION_DAYS",
		"WEBHOOK_EVENT_RETENTION_DAYS",
	} {
		for _, value := range []string{"0", "-1"} {
			t.Run(env+"="+value, func(t *testing.T) {
				setRequiredEnv(t)
				t.Setenv(env, value)
				_, err := config.Load()
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				if !strings.Contains(err.Error(), env) {
					t.Errorf("error should name %s: %v", env, err)
				}
			})
		}
	}
}

func TestWarnPlaintextTransport(t *testing.T) {
	const secret = "s3cretpass"
	plaintextPG := "postgres://u:" + secret + "@db:5432/app?sslmode=disable"
	securePG := "postgres://u:" + secret + "@db:5432/app?sslmode=verify-full"

	build := func(env, pg, redis, amqp string) config.Config {
		return config.Config{
			Env:      env,
			Postgres: config.PostgresConfig{URL: pg, WorkerURL: pg, WebhookURL: pg, UnscopedURL: pg},
			Redis:    config.RedisConfig{URL: redis},
			RabbitMQ: config.RabbitMQConfig{URL: amqp},
		}
	}
	warnings := func(cfg config.Config) string {
		var buf bytes.Buffer
		cfg.WarnPlaintextTransport(slog.New(slog.NewTextHandler(&buf, nil)))
		return buf.String()
	}

	t.Run("development stays quiet", func(t *testing.T) {
		if out := warnings(build("development", plaintextPG, "redis://r", "amqp://m")); out != "" {
			t.Errorf("unexpected output: %s", out)
		}
	})

	t.Run("encrypted transport stays quiet", func(t *testing.T) {
		if out := warnings(build("production", securePG, "rediss://r", "amqps://m")); out != "" {
			t.Errorf("unexpected output: %s", out)
		}
	})

	t.Run("each plaintext datastore warns once by name, never by value", func(t *testing.T) {
		out := warnings(build("staging", plaintextPG, "redis://u:"+secret+"@r:6379/0", "amqp://u:"+secret+"@m:5672/"))
		for _, env := range []string{
			"POSTGRES_APP_URL", "POSTGRES_WORKER_URL", "POSTGRES_WEBHOOK_URL",
			"POSTGRES_UNSCOPED_URL", "REDIS_URL", "RABBITMQ_URL",
		} {
			if got := strings.Count(out, "env="+env); got != 1 {
				t.Errorf("want one warning naming %s, got %d", env, got)
			}
		}
		if got := strings.Count(out, "plaintext datastore transport outside development"); got != 6 {
			t.Errorf("want 6 warnings, got %d:\n%s", got, out)
		}
		if strings.Contains(out, secret) || strings.Contains(out, "@db") {
			t.Errorf("a warning leaked a connection string: %s", out)
		}
	})

	for sslmode, want := range map[string]bool{
		"":            true,
		"disable":     true,
		"allow":       true,
		"prefer":      true,
		"require":     false,
		"verify-ca":   false,
		"verify-full": false,
	} {
		t.Run("sslmode="+sslmode, func(t *testing.T) {
			url := "postgres://db:5432/app"
			if sslmode != "" {
				url += "?sslmode=" + sslmode
			}
			out := warnings(build("production", url, "rediss://r", "amqps://m"))
			if got := strings.Contains(out, "POSTGRES_APP_URL"); got != want {
				t.Errorf("warned = %v, want %v", got, want)
			}
		})
	}
}
