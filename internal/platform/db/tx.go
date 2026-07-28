package db

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type querierKey struct{}

// WithQuerier stores a Querier in ctx so service methods behind the organization
// middleware can use the RLS-scoped transaction instead of the raw pool.
func WithQuerier(ctx context.Context, q Querier) context.Context {
	return context.WithValue(ctx, querierKey{}, q)
}

// QuerierFromContext returns the Querier stored by WithQuerier, or fallback
// (typically *pgxpool.Pool) when none is present.
func QuerierFromContext(ctx context.Context, fallback Querier) Querier {
	if q, ok := ctx.Value(querierKey{}).(Querier); ok && q != nil {
		return q
	}
	return fallback
}

// WithoutQuerier clears any Querier stashed in ctx by WithQuerier. Use it
// before deriving a context for a goroutine that will outlive (or run
// concurrently with) the caller — e.g. a fire-and-forget background op
// started with context.WithoutCancel, which preserves every value on the
// parent context, including a transaction that only the caller's own
// goroutine may safely use. A *pgx.Tx is not safe for concurrent use from
// multiple goroutines; leaving it reachable via QuerierFromContext lets a
// background goroutine grab the same in-flight transaction the request
// handler is still using, corrupting pgx's per-connection statement cache.
// Storing a literal nil clears the value: a later type assertion against a
// nil `any` always fails, so QuerierFromContext correctly falls back to the
// pool instead of picking up a stale or already-nil Querier.
func WithoutQuerier(ctx context.Context) context.Context {
	return context.WithValue(ctx, querierKey{}, nil)
}

// Querier is satisfied by both *pgxpool.Pool and pgx.Tx. Repository methods
// accept a Querier instead of a concrete pool, so the same repository code
// runs whether it's called standalone or inside WithTx — this is what lets
// a service layer compose multiple repository calls into one transaction
// without every repository method needing two versions of itself.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// WithTx runs fn inside a transaction. fn receives a Querier (the *pgx.Tx)
// to pass into repository methods. If fn returns an error, the transaction
// is rolled back; otherwise it's committed.
//
// Usage in a service method that needs atomicity across two repo calls:
//
//	err := db.WithTx(ctx, pool, func(tx db.Querier) error {
//	    if err := s.repo.CreateOrg(ctx, tx, org); err != nil {
//	        return err
//	    }
//	    return s.repo.CreateDefaultMembership(ctx, tx, org.ID, ownerID)
//	})
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx Querier) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	// Without this, a panic inside fn unwinds past the error-handling
	// rollback below entirely — neither Commit nor Rollback ever runs, so
	// the leased pool connection is never released even though an outer
	// recovery middleware catches the panic and the request survives.
	// Rolling back here and re-panicking guarantees cleanup without
	// swallowing the panic itself.
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			return fmt.Errorf("tx failed: %w (rollback also failed: %v)", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}

type pendingEventsKey struct{}

// pendingEventsQueue guards its slice with a mutex — QueueEvent may be
// called from goroutines spawned within the same request/transaction, and
// appending to a shared slice without synchronization is a data race.
type pendingEventsQueue struct {
	mu     sync.Mutex
	events []func()
}

// WithPendingEvents installs an empty pending-events queue into ctx, scoped
// to one request/transaction. Call once, before the transaction begins.
func WithPendingEvents(ctx context.Context) context.Context {
	return context.WithValue(ctx, pendingEventsKey{}, &pendingEventsQueue{})
}

// QueueEvent defers fn until FlushPendingEvents runs — used to publish a
// domain event only after its enclosing transaction has actually committed,
// instead of interleaved with the writes that produced it (publishing
// before commit would announce state that might still roll back). If ctx
// carries no queue (no enclosing transaction set one up), fn runs
// immediately — there's nothing to defer it past.
func QueueEvent(ctx context.Context, fn func()) {
	q, ok := ctx.Value(pendingEventsKey{}).(*pendingEventsQueue)
	if !ok {
		fn()
		return
	}
	q.mu.Lock()
	q.events = append(q.events, fn)
	q.mu.Unlock()
}

// FlushPendingEvents runs every event queued via QueueEvent, in order, then
// clears the queue. Call only after a successful commit — never after a
// rollback, since the queued events describe state that was just discarded.
func FlushPendingEvents(ctx context.Context) {
	q, ok := ctx.Value(pendingEventsKey{}).(*pendingEventsQueue)
	if !ok {
		return
	}
	// Copy the queue out and release the lock before running events —
	// an event running QueueEvent (re-entrant) would otherwise deadlock.
	q.mu.Lock()
	events := q.events
	q.events = nil
	q.mu.Unlock()

	for _, fn := range events {
		fn()
	}
}
