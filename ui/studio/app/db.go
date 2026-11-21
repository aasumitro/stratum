package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/studio/internal/connect"
	"github.com/aasumitro/stratum/studio/internal/query"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotOperator refuses a change on a connection that is not a person's own
// operator role, because the audit trail could not attribute it to anyone.
var ErrNotOperator = errors.New("this connection is not an operator role; changes would not be audited")

// requireOperator must be the first statement of every mutating service method:
// it is what guarantees a change made through Studio is attributed to a person.
// The app/operator_guard_test.go AST test fails when a method skips it.
func requireOperator(projects *ProjectService, pool *connect.PostgresPool, projectID string) error {
	isOperator, err := withProjectDB(projects, pool, projectID, "requireOperator", 10*time.Second, query.IsOperator)
	if err != nil {
		return err
	}
	if !isOperator {
		return ErrNotOperator
	}
	return nil
}

// withProjectDB collapses the projects.Get -> pool.Get -> context.WithTimeout
// prelude repeated across every project-scoped query. Any error from projects,
// pool, or fn is wrapped as "label: err".
func withProjectDB[T any](
	projects *ProjectService,
	pool *connect.PostgresPool,
	projectID, label string,
	timeout time.Duration,
	fn func(ctx context.Context, db *pgxpool.Pool) (T, error),
) (T, error) {
	var zero T

	project, err := projects.Get(projectID)
	if err != nil {
		return zero, fmt.Errorf("%s: %w", label, err)
	}

	db, err := pool.Get(projectID, project.DBDSN)
	if err != nil {
		return zero, fmt.Errorf("%s: %w", label, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	out, err := fn(ctx, db)
	if err != nil {
		return zero, fmt.Errorf("%s: %w", label, err)
	}
	return out, nil
}

// withProjectPool resolves the project's pool without imposing a shared
// context/timeout, for callers that need to manage their own (e.g. a
// multi-batch operation with a fresh deadline per batch).
func withProjectPool(
	projects *ProjectService,
	pool *connect.PostgresPool,
	projectID, label string,
) (*pgxpool.Pool, error) {
	project, err := projects.Get(projectID)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	db, err := pool.Get(projectID, project.DBDSN)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	return db, nil
}

// withProjectDBErr is withProjectDB for calls that only return an error.
func withProjectDBErr(
	projects *ProjectService,
	pool *connect.PostgresPool,
	projectID, label string,
	timeout time.Duration,
	fn func(ctx context.Context, db *pgxpool.Pool) error,
) error {
	_, err := withProjectDB(projects, pool, projectID, label, timeout,
		func(ctx context.Context, db *pgxpool.Pool) (struct{}, error) {
			return struct{}{}, fn(ctx, db)
		})
	return err
}
