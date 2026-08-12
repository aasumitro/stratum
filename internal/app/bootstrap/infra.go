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

// Close releases every connection SetupInfra opened, in reverse order
// (mq, redis, postgres, otel).
// Errors are discarded: shutdown
// is best-effort by convention throughout this codebase.
func (i *Infra) Close(ctx context.Context) {
	_ = i.mqShutdown()
	i.Pool.Close()
	_ = i.redisShutdown()
	_ = i.otelShutdown(ctx)
}
