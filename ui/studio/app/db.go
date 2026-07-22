package app

import (
	"context"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/studio/internal/connect"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
