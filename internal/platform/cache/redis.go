// Package cache wraps github.com/redis/go-redis/v9 with OTel
// instrumentation and per-module key namespacing. One physical Redis
// connection pool is shared process-wide (constructed once in main.go);
// each module gets a NamespacedCache view over it (see namespace.go) so
// modules can never collide on key names without coordinating through a
// platform-level change.
package cache

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"

	"github.com/aasumitro/stratum/internal/platform/config"
)

// NewClient parses cfg.URL, instruments the client with OTel tracing and
// metrics (redisotel — wired automatically into whatever TracerProvider/
// MeterProvider otel.Setup registered globally, since redisotel reads
// from otel.GetTracerProvider()/GetMeterProvider() rather than taking
// them as explicit arguments), and pings once so a bad connection string
// fails at boot rather than on first cache access.
func NewClient(ctx context.Context, cfg config.RedisConfig) (*redis.Client, error) {
	opts, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parsing redis url: %w", err)
	}

	client := redis.NewClient(opts)

	if err := errors.Join(
		redisotel.InstrumentTracing(client),
		redisotel.InstrumentMetrics(client),
	); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("instrumenting redis client: %w", err)
	}

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("pinging redis: %w", err)
	}

	return client, nil
}
