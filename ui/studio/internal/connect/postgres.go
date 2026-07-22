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
	pools map[string]*pgxpool.Pool
}

func NewPostgresPool() *PostgresPool {
	return &PostgresPool{pools: make(map[string]*pgxpool.Pool)}
}

func (p *PostgresPool) Get(projectID, dsn string) (*pgxpool.Pool, error) {
	p.mu.Lock()
	if pool, ok := p.pools[projectID]; ok {
		p.mu.Unlock()
		return pool, nil
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

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect.Get: open pool: %s", RedactDSN(err.Error()))
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.pools[projectID]; ok {
		// Another concurrent call for the same never-seen project won the
		// race and already stored its pool — keep theirs, discard ours.
		pool.Close()
		return existing, nil
	}
	p.pools[projectID] = pool
	return pool, nil
}

func (p *PostgresPool) Invalidate(projectID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pool, ok := p.pools[projectID]; ok {
		pool.Close()
		delete(p.pools, projectID)
	}
}

func (p *PostgresPool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, pool := range p.pools {
		pool.Close()
		delete(p.pools, id)
	}
}
