package db

import (
	"context"
	"fmt"

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

// WithPendingEvents installs an empty pending-events queue into ctx, scoped
// to one request/transaction. Call once, before the transaction begins.
func WithPendingEvents(ctx context.Context) context.Context {
	q := make([]func(), 0)
	return context.WithValue(ctx, pendingEventsKey{}, &q)
}

// QueueEvent defers fn until FlushPendingEvents runs — used to publish a
// domain event only after its enclosing transaction has actually committed,
// instead of interleaved with the writes that produced it (publishing
// before commit would announce state that might still roll back). If ctx
// carries no queue (no enclosing transaction set one up), fn runs
// immediately — there's nothing to defer it past.
func QueueEvent(ctx context.Context, fn func()) {
	if q, ok := ctx.Value(pendingEventsKey{}).(*[]func()); ok {
		*q = append(*q, fn)
		return
	}
	fn()
}

// FlushPendingEvents runs every event queued via QueueEvent, in order, then
// clears the queue. Call only after a successful commit — never after a
// rollback, since the queued events describe state that was just discarded.
func FlushPendingEvents(ctx context.Context) {
	if q, ok := ctx.Value(pendingEventsKey{}).(*[]func()); ok {
		for _, fn := range *q {
			fn()
		}
		*q = nil
	}
}
