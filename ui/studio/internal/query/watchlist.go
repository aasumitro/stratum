package query

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type WatchlistItem struct {
	OrganizationID   string
	OrganizationName string
	OrganizationSlug string
	PlanName         string
	Status           string
	TrialEnd         *time.Time
	PeriodEnd        *time.Time
	DaysRemaining    int
}

func GetTrialsEndingSoon(ctx context.Context, pool *pgxpool.Pool) ([]WatchlistItem, error) {
	rows, err := pool.Query(ctx, `
		SELECT t.id::text, t.name, t.slug,
		       COALESCE(p.name, s.plan, '') AS plan_name,
		       s.status, s.trial_end, s.period_end,
		       EXTRACT(DAY FROM s.trial_end - now())::int AS days_remaining
		FROM billing.subscriptions s
		JOIN organization.organizations t ON t.id::text = s.subject_id
		LEFT JOIN billing.plans p ON p.id = s.plan
		WHERE s.status = 'trialing'
		  AND s.trial_end IS NOT NULL
		  AND s.trial_end <= now() + INTERVAL '7 days'
		ORDER BY s.trial_end ASC
		LIMIT 100
	`)
	if err != nil {
		return nil, fmt.Errorf("query.GetTrialsEndingSoon: %w", err)
	}
	defer rows.Close()
	return scanWatchlistRows(rows, "query.GetTrialsEndingSoon")
}

func GetPastDue(ctx context.Context, pool *pgxpool.Pool) ([]WatchlistItem, error) {
	rows, err := pool.Query(ctx, `
		SELECT t.id::text, t.name, t.slug,
		       COALESCE(p.name, s.plan, '') AS plan_name,
		       s.status, s.trial_end, s.period_end,
		       -EXTRACT(DAY FROM now() - s.period_end)::int AS days_remaining
		FROM billing.subscriptions s
		JOIN organization.organizations t ON t.id::text = s.subject_id
		LEFT JOIN billing.plans p ON p.id = s.plan
		WHERE s.status = 'past_due'
		ORDER BY s.period_end ASC
		LIMIT 100
	`)
	if err != nil {
		return nil, fmt.Errorf("query.GetPastDue: %w", err)
	}
	defer rows.Close()
	return scanWatchlistRows(rows, "query.GetPastDue")
}

func GetRenewalsDue(ctx context.Context, pool *pgxpool.Pool) ([]WatchlistItem, error) {
	rows, err := pool.Query(ctx, `
		SELECT t.id::text, t.name, t.slug,
		       COALESCE(p.name, s.plan, '') AS plan_name,
		       s.status, s.trial_end, s.period_end,
		       EXTRACT(DAY FROM s.period_end - now())::int AS days_remaining
		FROM billing.subscriptions s
		JOIN organization.organizations t ON t.id::text = s.subject_id
		LEFT JOIN billing.plans p ON p.id = s.plan
		WHERE s.status = 'active'
		  AND s.period_end IS NOT NULL
		  AND s.period_end >= now()
		  AND s.period_end <= now() + INTERVAL '7 days'
		ORDER BY s.period_end ASC
		LIMIT 100
	`)
	if err != nil {
		return nil, fmt.Errorf("query.GetRenewalsDue: %w", err)
	}
	defer rows.Close()
	return scanWatchlistRows(rows, "query.GetRenewalsDue")
}

func GetCancellationsTakingEffect(ctx context.Context, pool *pgxpool.Pool) ([]WatchlistItem, error) {
	rows, err := pool.Query(ctx, `
		SELECT t.id::text, t.name, t.slug,
		       COALESCE(p.name, s.plan, '') AS plan_name,
		       s.status, s.trial_end, s.period_end,
		       EXTRACT(DAY FROM s.period_end - now())::int AS days_remaining
		FROM billing.subscriptions s
		JOIN organization.organizations t ON t.id::text = s.subject_id
		LEFT JOIN billing.plans p ON p.id = s.plan
		WHERE s.status = 'cancelled'
		  AND s.period_end IS NOT NULL
		  AND s.period_end >= now()
		ORDER BY s.period_end ASC
		LIMIT 100
	`)
	if err != nil {
		return nil, fmt.Errorf("query.GetCancellationsTakingEffect: %w", err)
	}
	defer rows.Close()
	return scanWatchlistRows(rows, "query.GetCancellationsTakingEffect")
}

func scanWatchlistRows(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}, caller string) ([]WatchlistItem, error) {
	var out []WatchlistItem
	for rows.Next() {
		var item WatchlistItem
		if err := rows.Scan(
			&item.OrganizationID, &item.OrganizationName, &item.OrganizationSlug,
			&item.PlanName, &item.Status, &item.TrialEnd, &item.PeriodEnd,
			&item.DaysRemaining,
		); err != nil {
			return nil, fmt.Errorf("%s: scan: %w", caller, err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: rows: %w", caller, err)
	}
	return out, nil
}
