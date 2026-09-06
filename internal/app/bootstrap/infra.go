// Package bootstrap builds the composition root for both the API and
// worker binaries: shared infrastructure connections (Infra), each
// binary's domain module wiring (APIModules/WorkerModules), and the
// binary-specific serving surface (the HTTP router for API, the consumer
// list for worker). internal/app/api.go and internal/app/worker.go stay
// thin — infra -> modules -> serve -> shutdown.
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/aasumitro/stratum/internal/platform/cache"
	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/platform/db"
	"github.com/aasumitro/stratum/internal/platform/logger"
	"github.com/aasumitro/stratum/internal/platform/messaging"
	"github.com/aasumitro/stratum/internal/platform/otel"
)

// EnvDevelopment is the config.Config.Env value that relaxes
// production-only behavior (OTel local sampling/TLS, CORS wildcard, gin
// debug mode). Single source of truth shared by both binaries.
const EnvDevelopment = "development"

// Infra bundles every external-system connection shared by both the API
// and worker binaries. Binary-specific setup (the API's storage client
// and billing-delay exchange declaration, the worker's delay-queue
// topology) stays in RunAPI/RunWorker.
type Infra struct {
	Cfg   *config.Config
	Log   *slog.Logger
	Pool  *pgxpool.Pool
	Redis *goredis.Client

	// WebhookPool is a BYPASSRLS connection pool used by exactly one call
	// site (billing's processWebhook) to apply a webhook-driven payment
	// confirmation, which arrives with no authenticated org context to
	// satisfy RLS. Only RunAPI opens it — webhooks never reach cmd/worker —
	// so it stays nil on a worker-built Infra.
	WebhookPool *pgxpool.Pool

	// BackgroundPool is a small connection pool, credentialed identically to
	// Pool (same stratum_app role/URL), reserved for background writes that
	// must not compete with request-handler connections for Pool's slots:
	// the audit writer's batch flush and the OnAuth hook's last_seen_at
	// update. Only RunAPI opens it — cmd/worker has no request path for a
	// second pool to protect — so it stays nil on a worker-built Infra.
	BackgroundPool *pgxpool.Pool

	MQConn      *messaging.Connection
	MQPublisher *messaging.Publisher

	otelShutdown  func(context.Context) error
	redisShutdown func() error
	mqShutdown    func() error
}

// SetupInfra dials OTel, Postgres, Redis, and RabbitMQ in that order,
// unwinding anything already opened if a later step fails. Call
// Infra.Close on the successful result to release everything in reverse.
func SetupInfra(ctx context.Context, cfg *config.Config, postgresURL string) (*Infra, error) {
	otelProviders, otelShutdown, err := otel.Setup(
		ctx, cfg.OTel, cfg.ServiceName, cfg.ServiceVersion, cfg.Env == EnvDevelopment,
	)
	if err != nil {
		return nil, fmt.Errorf("setting up otel: %w", err)
	}

	log := logger.New(cfg.Log.Level, cfg.Log.Format, otelProviders.SlogHandler())

	pgCfg := cfg.Postgres
	pgCfg.URL = postgresURL
	pool, err := db.NewPostgresPool(ctx, pgCfg)
	if err != nil {
		_ = otelShutdown(ctx)
		return nil, fmt.Errorf("setting up postgres: %w", err)
	}

	redisClient, redisShutdown, err := cache.Setup(ctx, cfg.Redis)
	if err != nil {
		pool.Close()
		_ = otelShutdown(ctx)
		return nil, fmt.Errorf("setting up redis: %w", err)
	}

	mqConn, mqPublisher, mqShutdown, err := messaging.Setup(ctx, cfg.RabbitMQ.URL, log)
	if err != nil {
		_ = redisShutdown()
		pool.Close()
		_ = otelShutdown(ctx)
		return nil, fmt.Errorf("setting up rabbitmq: %w", err)
	}

	return &Infra{
		Cfg:           cfg,
		Log:           log,
		Pool:          pool,
		Redis:         redisClient,
		MQConn:        mqConn,
		MQPublisher:   mqPublisher,
		otelShutdown:  otelShutdown,
		redisShutdown: redisShutdown,
		mqShutdown:    mqShutdown,
	}, nil
}

// OpenWebhookPool opens Infra.WebhookPool, credentialed for stratum_webhook
// (cfg.Postgres.WebhookURL). Called only by RunAPI, after SetupInfra
// succeeds — webhook confirmations never reach cmd/worker, so RunWorker
// never calls this and WebhookPool stays nil on that Infra. Assigning the
// opened pool to infra.WebhookPool before returning means RunAPI's
// already-deferred Infra.Close cleans it up on any later failure, with no
// separate unwind path needed.
func OpenWebhookPool(ctx context.Context, infra *Infra) error {
	pgCfg := infra.Cfg.Postgres
	pgCfg.URL = pgCfg.WebhookURL
	pool, err := db.NewPostgresPool(ctx, pgCfg)
	if err != nil {
		return fmt.Errorf("setting up webhook postgres pool: %w", err)
	}
	infra.WebhookPool = pool
	return nil
}

// validateBackgroundPoolSize rejects a background-pool size that would
// either starve background writes (<= 0) or let the background pool claim
// as many or more server connections than the main request pool
// (background >= main), defeating the connection-slot split the two pools
// exist to provide.
func validateBackgroundPoolSize(background, main int32) error {
	if background <= 0 {
		return fmt.Errorf("POSTGRES_BACKGROUND_MAX_OPEN_CONNS must be > 0")
	}
	if background >= main {
		return fmt.Errorf(
			"POSTGRES_BACKGROUND_MAX_OPEN_CONNS (%d) must be smaller than POSTGRES_MAX_OPEN_CONNS (%d)",
			background, main,
		)
	}
	return nil
}

// OpenBackgroundPool opens Infra.BackgroundPool, reusing the same
// stratum_app role/URL as Infra.Pool (cfg.Postgres.URL) — this is a
// connection-slot split, not a privilege split, so only MaxOpenConns
// differs. Called only by RunAPI, after SetupInfra succeeds — cmd/worker
// has no request path for a second pool to protect, so RunWorker never
// calls this and BackgroundPool stays nil on that Infra. Assigning the
// opened pool to infra.BackgroundPool before returning means RunAPI's
// already-deferred Infra.Close cleans it up on any later failure, with no
// separate unwind path needed.
func OpenBackgroundPool(ctx context.Context, infra *Infra) error {
	if err := validateBackgroundPoolSize(
		infra.Cfg.Postgres.BackgroundMaxOpenConns, infra.Cfg.Postgres.MaxOpenConns,
	); err != nil {
		return fmt.Errorf("bootstrap.OpenBackgroundPool: %w", err)
	}

	pgCfg := infra.Cfg.Postgres
	pgCfg.MaxOpenConns = infra.Cfg.Postgres.BackgroundMaxOpenConns
	pool, err := db.NewPostgresPool(ctx, pgCfg)
	if err != nil {
		return fmt.Errorf("setting up background postgres pool: %w", err)
	}
	infra.BackgroundPool = pool
	return nil
}

// Close releases every connection SetupInfra (and, on the API binary,
// OpenWebhookPool/OpenBackgroundPool) opened, in reverse order (background
// pool, webhook pool, mq, redis, postgres, otel). BackgroundPool and
// WebhookPool are nil-checked since RunWorker's Infra never opens either.
// Errors are discarded: shutdown
// is best-effort by convention throughout this codebase.
func (i *Infra) Close(ctx context.Context) {
	if i.BackgroundPool != nil {
		i.BackgroundPool.Close()
	}
	if i.WebhookPool != nil {
		i.WebhookPool.Close()
	}
	_ = i.mqShutdown()
	i.Pool.Close()
	_ = i.redisShutdown()
	_ = i.otelShutdown(ctx)
}
