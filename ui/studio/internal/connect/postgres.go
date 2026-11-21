package connect

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresPool caches one pgxpool.Pool per project ID.
// Connections are established lazily and reused across calls.
type PostgresPool struct {
	mu    sync.Mutex
	pools map[string]cachedPool
}

// cachedPool remembers the DSN its pool was opened with. A project's DSN decides
// whose name its changes are recorded under, so a pool opened with an older DSN must
// never answer for a newer one.
type cachedPool struct {
	pool *pgxpool.Pool
	dsn  string
}

func NewPostgresPool() *PostgresPool {
	return &PostgresPool{pools: make(map[string]cachedPool)}
}

// Get returns the project's pool for dsn, replacing a cached pool opened with a
// different one. Every caller passes the project's current DSN, so a call that read
// the old DSN just before an edit can only displace the new pool until the next
// call, which puts it back.
func (p *PostgresPool) Get(projectID, dsn string) (*pgxpool.Pool, error) {
	p.mu.Lock()
	if cached, ok := p.pools[projectID]; ok && cached.dsn == dsn {
		p.mu.Unlock()
		return cached.pool, nil
	}
	p.mu.Unlock()

	// Connect outside the lock — a slow/unreachable project must not stall
	// every other project's pool access for up to 10s.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("connect.Get: parse config: %s", RedactDSN(err.Error()))
	}
	cfg.MaxConns = 3
	cfg.MinConns = 1
	// The audit trail records this as the source of an operator's changes.
	cfg.ConnConfig.RuntimeParams["application_name"] = "stratum-studio"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect.Get: open pool: %s", RedactDSN(err.Error()))
	}

	p.mu.Lock()
	existing, ok := p.pools[projectID]
	if ok && existing.dsn == dsn {
		// Another concurrent call for the same DSN won the race and already
		// stored its pool — keep theirs, discard ours.
		p.mu.Unlock()
		pool.Close()
		return existing.pool, nil
	}
	p.pools[projectID] = cachedPool{pool: pool, dsn: dsn}
	p.mu.Unlock()

	// Close waits for in-flight queries, which must not stall other projects.
	if ok {
		existing.pool.Close()
	}
	return pool, nil
}

func (p *PostgresPool) Invalidate(projectID string) {
	p.mu.Lock()
	existing, ok := p.pools[projectID]
	delete(p.pools, projectID)
	p.mu.Unlock()

	// Close waits for in-flight queries, which must not stall other projects.
	if ok {
		existing.pool.Close()
	}
}

func (p *PostgresPool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, cached := range p.pools {
		cached.pool.Close()
		delete(p.pools, id)
	}
}
