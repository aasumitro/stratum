// Package config loads typed application configuration from environment
// variables. All config lives in one struct so every dependency
// (db, cache, messaging, http) is constructed from a single source of truth
// passed explicitly through main.go — never read from os.Getenv() deep
// inside a module.
package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// placeholderWebhookEncryptionKey is the unmistakable non-secret value .env.example ships for
// WEBHOOK_SECRET_ENCRYPTION_KEY. RequireSecretsOutsideDev rejects it explicitly outside
// development so a deployment that copied .env.example forward without generating a real key
// fails loudly at boot instead of silently encrypting webhook secrets under a passphrase that's
// public in the repository.
const placeholderWebhookEncryptionKey = "CHANGE_ME_generate_with_openssl_rand_base64_32"

// Config is the root configuration struct. Nested structs group config
// by infrastructure concern, mirroring the platform/ package layout.
type Config struct {
	Env            string `env:"APP_ENV" envDefault:"development"`
	Port           string `env:"PORT" envDefault:"8080"`
	AppURL         string `env:"APP_URL" envDefault:"http://localhost:3000"`
	ServiceName    string `env:"SERVICE_NAME" envDefault:"stratum"`
	ServiceVersion string `env:"SERVICE_VERSION" envDefault:"dev"`
	CORSOrigins    string `env:"CORS_ORIGINS"` // comma-separated, empty = allow all

	// TrustedProxies lists the CIDR ranges of reverse proxies/load balancers
	// this service sits directly behind (an ingress controller's pod CIDR,
	// an ALB/GCLB's subnet, Cloudflare's published ranges, ...). Only hops
	// in this list are trusted to supply X-Forwarded-For/X-Real-IP; empty
	// means "trust nobody", so gin's Context.ClientIP() — and everything
	// keyed on it, including NewIPAllowlistMiddleware and the webhook rate
	// limiter's ByClientIP — falls back to the raw TCP peer address. Set
	// this whenever a real proxy/LB/ingress terminates connections before
	// they reach this process, or every forwarded request resolves to the
	// proxy's own IP instead of the real client's (breaking IP allowlists
	// and letting a spoofed header evade per-IP rate limiting).
	TrustedProxies []string `env:"TRUSTED_PROXIES" envSeparator:","`

	AuditRetentionDays  int `env:"AUDIT_RETENTION_DAYS" envDefault:"30"`
	OutboxRetentionDays int `env:"OUTBOX_RETENTION_DAYS" envDefault:"30"`

	// StatsToken gates GET /health/stats (goroutine/heap/pool internals —
	// a reconnaissance surface if reachable from the public internet).
	// Empty = unauthenticated, same graceful-degrade convention as the
	// webhook secrets below — fine for local dev, but every non-dev
	// deployment should set this. Studio sends it back as X-Stats-Token.
	StatsToken string `env:"STATS_TOKEN"`

	// GeoIPDBPath is the filesystem path to a MaxMind GeoLite2/GeoIP2
	// Country .mmdb file (internal/platform/geoip) — the trusted source for
	// an organization's billing country/currency, resolved from the
	// request's IP instead of a client-supplied field. Empty is only valid
	// in development, where geoip.Resolver substitutes an
	// X-Debug-Country-Code header instead (see RequireGeoIPDBOutsideDev).
	GeoIPDBPath string `env:"GEOIP_DB_PATH"`

	// WebhookSecretEncryptionKey is the pgcrypto (pgp_sym_encrypt/pgp_sym_decrypt) passphrase
	// used to encrypt organization.webhook_endpoints' outbound signing secrets at rest. Unlike
	// the inbound webhook secrets below, an empty value has no safe meaning here — there's no
	// legitimate "skip encryption" mode — so this is required in every environment, including
	// development, not gated behind RequireSecretsOutsideDev like STRIPE_WEBHOOK_SECRET etc.
	WebhookSecretEncryptionKey string `env:"WEBHOOK_SECRET_ENCRYPTION_KEY,required,notEmpty"`
	// WebhookSecretEncryptionKeyPrevious is the prior pgcrypto passphrase, set
	// alongside WebhookSecretEncryptionKeyVersion only while a key rotation is
	// in progress. Empty (the default) means no rotation is in progress — unlike
	// WebhookSecretEncryptionKey, an empty value here is a legitimate steady
	// state, so this field is NOT tagged notEmpty.
	WebhookSecretEncryptionKeyPrevious string `env:"WEBHOOK_SECRET_ENCRYPTION_KEY_PREVIOUS"`
	// WebhookSecretEncryptionKeyVersion is the version number an operator bumps
	// by hand alongside setting WebhookSecretEncryptionKeyPrevious when rotating.
	WebhookSecretEncryptionKeyVersion int `env:"WEBHOOK_SECRET_ENCRYPTION_KEY_VERSION" envDefault:"1"`

	Postgres PostgresConfig
	Redis    RedisConfig
	RabbitMQ RabbitMQConfig
	Auth     AuthConfig
	Log      LogConfig
	OTel     OTelConfig
	Stripe   StripeConfig
	Xendit   XenditConfig
	SMTP     SMTPConfig
	Storage  StorageConfig
}

type PostgresConfig struct {
	URL       string `env:"POSTGRES_APP_URL,required"`
	WorkerURL string `env:"POSTGRES_WORKER_URL,required"`
	// WebhookURL is required on both binaries because both load this same
	// struct, but only cmd/api ever opens a pool with it (see
	// bootstrap.SetupInfra/RunAPI) — cmd/worker never serves webhooks.
	WebhookURL   string `env:"POSTGRES_WEBHOOK_URL,required"`
	MaxOpenConns int32  `env:"POSTGRES_MAX_OPEN_CONNS" envDefault:"20"`
	// BackgroundMaxOpenConns sizes Infra.BackgroundPool (cmd/api only) — a
	// small pool isolating background writes (the audit writer, the OnAuth
	// last_seen_at update) from request-path connection contention on
	// Infra.Pool. Same stratum_app role/URL as Infra.Pool; only pool size
	// differs.
	BackgroundMaxOpenConns int32         `env:"POSTGRES_BACKGROUND_MAX_OPEN_CONNS" envDefault:"5"`
	MaxIdleTime            time.Duration `env:"POSTGRES_MAX_IDLE_TIME" envDefault:"5m"`
	// StatementTimeout is passed to Postgres verbatim as the
	// statement_timeout runtime parameter on every pooled connection, so a
	// runaway query is aborted instead of holding a connection open
	// indefinitely. Postgres parses the value (e.g. "30s", "30000", "0"),
	// not this code; "0" disables the timeout entirely.
	StatementTimeout string `env:"POSTGRES_STATEMENT_TIMEOUT" envDefault:"30s"`
}

type RedisConfig struct {
	URL string `env:"REDIS_URL,required"`
}

type RabbitMQConfig struct {
	URL string `env:"RABBITMQ_URL,required"`
}

// AuthConfig holds settings for validating third-party tokens
// (Supabase or Clerk). JWKSURL is the only required field — we verify
// tokens via their published JSON Web Key Set rather than a shared secret.
type AuthConfig struct {
	JWKSURL  string `env:"AUTH_JWKS_URL,required"`
	Issuer   string `env:"AUTH_ISSUER,required"`
	Audience string `env:"AUTH_AUDIENCE"`
	AdminURL string `env:"AUTH_ADMIN_URL"` // e.g. https://xxx.supabase.co/auth/v1

	// AccessTokenMaxTTL is the maximum lifetime a Supabase-issued access token
	// can have in this deployment — must match or exceed the Supabase project's
	// configured JWT expiry. Used by account.revokeAllSessions to size the
	// per-user revocation epoch's TTL, since that epoch must outlive every
	// access token that could still be valid when it's written, not just the
	// token that triggered the revocation (see docs/02-getting-started.md).
	AccessTokenMaxTTL time.Duration `env:"AUTH_ACCESS_TOKEN_MAX_TTL" envDefault:"1h"`
	ServiceRoleKey    string        `env:"AUTH_SERVICE_ROLE_KEY"`   // Supabase service_role key
	WebhookSecret     string        `env:"SUPABASE_WEBHOOK_SECRET"` // shared secret header for the auth.users Database Webhook; empty = skip verification
}

type LogConfig struct {
	Level  string `env:"LOG_LEVEL" envDefault:"info"`
	Format string `env:"LOG_FORMAT" envDefault:"json"`
}

// OTelConfig holds OpenTelemetry collector settings. CollectorURL left
// empty disables telemetry entirely — see otel.Setup, which no-ops in
// that case. This matters for local dev when no collector is running.
type OTelConfig struct {
	CollectorURL string `env:"OTEL_COLLECTOR_URL"`
}

type StripeConfig struct {
	APIKey        string `env:"STRIPE_API_KEY"`
	WebhookSecret string `env:"STRIPE_WEBHOOK_SECRET"`
	SuccessURL    string `env:"STRIPE_SUCCESS_URL"`
	CancelURL     string `env:"STRIPE_CANCEL_URL"`
}

type XenditConfig struct {
	APIKey        string   `env:"XENDIT_API_KEY"`
	CallbackToken string   `env:"XENDIT_CALLBACK_TOKEN"`
	AllowedCIDRs  []string `env:"XENDIT_ALLOWED_CIDRS" envSeparator:","`
}

type SMTPConfig struct {
	Host     string `env:"SMTP_HOST"`
	Port     int    `env:"SMTP_PORT" envDefault:"587"`
	Username string `env:"SMTP_USERNAME"`
	Password string `env:"SMTP_PASSWORD"`
	FromName string `env:"SMTP_FROM_NAME" envDefault:"Stratum"`
}

type StorageConfig struct {
	URL string `env:"STORAGE_URL"`
}

// envDevelopment is Config.Env's "relax production-only checks" value —
// bootstrap.EnvDevelopment holds the same string for callers outside this
// package (api.go/worker.go, which can't import this unexported constant),
// but every check inside this package itself (Load's validation, both
// Require*OutsideDev methods) compares against this local constant instead
// of repeating the literal.
const envDevelopment = "development"

// Load reads environment variables into a Config, returning an error
// (not a panic) so main.go decides how to fail.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	switch cfg.Env {
	case envDevelopment, "staging", "production":
	default:
		return nil, fmt.Errorf("config: invalid APP_ENV %q (want development|staging|production)", cfg.Env)
	}

	return cfg, nil
}

// RequireSecretsOutsideDev enforces that every registered webhook route has
// its verification secret configured and that operational secrets (like
// STATS_TOKEN) are set once Env isn't "development". Each webhook handler
// treats an empty secret as "skip verification" — fine for local dev, but
// an unset secret in any other env means the route silently accepts
// unsigned/forged payloads that mutate real state (payment status, account
// email). Call this right after Load() so a missing secret fails startup
// instead of serving.
func (c *Config) RequireSecretsOutsideDev() error {
	if c.Env == envDevelopment {
		return nil
	}
	var missing []string
	if c.Stripe.WebhookSecret == "" {
		missing = append(missing, "STRIPE_WEBHOOK_SECRET")
	}
	if c.Xendit.CallbackToken == "" {
		missing = append(missing, "XENDIT_CALLBACK_TOKEN")
	}
	if len(c.Xendit.AllowedCIDRs) == 0 {
		missing = append(missing, "XENDIT_ALLOWED_CIDRS")
	}
	if c.Auth.WebhookSecret == "" {
		missing = append(missing, "SUPABASE_WEBHOOK_SECRET")
	}
	if c.StatsToken == "" {
		missing = append(missing, "STATS_TOKEN")
	}
	if c.WebhookSecretEncryptionKey == placeholderWebhookEncryptionKey {
		missing = append(missing, "WEBHOOK_SECRET_ENCRYPTION_KEY (still the .env.example placeholder value)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("config: missing required secret(s) outside development: %s", strings.Join(missing, ", "))
	}
	return nil
}

// RequireGeoIPDBOutsideDev enforces that a real GeoIP Country database is
// configured once Env isn't "development". Outside development, billing
// currency/tax resolution (POST /organizations, GET /references/plans|addons)
// depends on a working GeoIP lookup — geoip.Resolver's development-only
// X-Debug-Country-Code override doesn't apply there, so an unset path would
// silently resolve every request to the same empty-lookup fallback. Call
// this right after Load(), alongside RequireSecretsOutsideDev.
func (c *Config) RequireGeoIPDBOutsideDev() error {
	if c.Env == envDevelopment {
		return nil
	}
	if c.GeoIPDBPath == "" {
		return errors.New("config: GEOIP_DB_PATH required outside development")
	}
	return nil
}
