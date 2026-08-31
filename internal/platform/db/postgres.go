// Package db builds the single shared *pgxpool.Pool used by every module.
// All modules share one physical pool today — see internal/platform/db/tx.go
// for the transaction helper modules use instead of holding raw connections.
//
// Schema isolation between modules is enforced by convention (each module's
// SQL is hand-written with the schema-qualified table name, e.g.
// "org.organizations") rather than by separate search_path settings, so the
// query text itself documents the boundary and a code reviewer can catch a
// module reaching into another module's schema at review time.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/config"
)

// NewPostgresPool parses cfg into a pgxpool.Config, applies pool-sizing
// defaults, and establishes the pool. It pings once before returning so
// a misconfigured connection string fails fast at boot, not on first request.
func NewPostgresPool(ctx context.Context, cfg config.PostgresConfig) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parsing postgres url: %w", err)
	}

	// statement_timeout is applied by pgx as a RuntimeParam on every
	// connection it opens for this pool. The value is handed to Postgres
	// as-is ("30s", "30000", "0"); an unparseable value surfaces on the
	// boot Ping below. An empty string leaves the server default in place.
	if cfg.StatementTimeout != "" {
		if poolCfg.ConnConfig.RuntimeParams == nil {
			poolCfg.ConnConfig.RuntimeParams = map[string]string{}
		}
		poolCfg.ConnConfig.RuntimeParams["statement_timeout"] = cfg.StatementTimeout
	}

	poolCfg.MaxConns = cfg.MaxOpenConns
	poolCfg.MinConns = 2
	poolCfg.MaxConnIdleTime = cfg.MaxIdleTime
	poolCfg.MaxConnLifetime = time.Hour
	poolCfg.HealthCheckPeriod = time.Minute
	poolCfg.ConnConfig.Tracer = newQueryTracer()

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("creating postgres pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}

	return pool, nil
}
