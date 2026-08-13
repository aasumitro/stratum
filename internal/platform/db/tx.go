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

// HasQuerier reports whether ctx carries a Querier installed by WithQuerier —
// true inside an ambient transaction (an RLS-wrapped request, or a caller's
// own db.WithTx), false when only the fallback pool is available.
func HasQuerier(ctx context.Context) bool {
	_, ok := ctx.Value(querierKey{}).(Querier)
	return ok
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

// RequireTx panics if q is not an active transaction (*pgx.Tx). Call this
// at the top of any repository function issuing a `FOR UPDATE` lock — the
// lock only protects anything for the lifetime of the transaction that
// holds it; against the bare pool, the lock is acquired and released
// within the same statement, silently providing no protection at all.
// Panicking here turns a route-group-membership mistake (billingPay
// instead of billing, or a new handler in the wrong group) into an
// immediate, loud 500 instead of a race condition that only shows up
// under real concurrent load.
func RequireTx(q Querier) {
	if _, ok := q.(pgx.Tx); !ok {
		panic("db.RequireTx: FOR UPDATE query issued without an active transaction")
	}
}

// SetOrgContext configures the Postgres app.organization_id setting for the
// current transaction so RLS policies can evaluate it.
func SetOrgContext(ctx context.Context, q Querier, orgID string) error {
	_, err := q.Exec(ctx, "SELECT set_config('app.organization_id', $1, true)", orgID)
	return err
}
