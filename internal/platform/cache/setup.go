package cache

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/aasumitro/stratum/internal/platform/config"
)

// Setup connects to Redis and returns the shared client plus a shutdown
// func, mirroring otel.Setup and messaging.Setup's shape for consistency
// across platform/ packages. Modules call NewNamespace(client, "modname")
// and/or NewRateLimiter(client, "modname") on the returned client rather
// than receiving raw access to it — see namespace.go and ratelimit.go.
func Setup(ctx context.Context, cfg config.RedisConfig) (*redis.Client, func() error, error) {
	client, err := NewClient(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("setting up redis: %w", err)
	}

	shutdown := func() error {
		return client.Close()
	}

	return client, shutdown, nil
}
