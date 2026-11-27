package query

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ReasonCount struct {
	Reason string
	Count  int64
}

type CancellationDetail struct {
	Reason  string
	Details string
	Plan    string
	At      time.Time
}

// CancellationReport is what owners said when they cancelled: how often each
// reason was picked, and the latest free-text details.
type CancellationReport struct {
	Counts []ReasonCount
	Latest []CancellationDetail
}

// cancellationLatestLimit caps the free-text details listed.
const cancellationLatestLimit = 50

// GetCancellationReasons reads the cancellation requests of the last days from
// billing.subscription_history (action 'cancel', metadata.reason). A request
// later undone still counts: the reason was given.
func GetCancellationReasons(ctx context.Context, pool *pgxpool.Pool, days int) (*CancellationReport, error) {
	r := &CancellationReport{Counts: []ReasonCount{}, Latest: []CancellationDetail{}}

	rows, err := pool.Query(ctx, `
		SELECT metadata->>'reason', count(*)
		FROM billing.subscription_history
		WHERE action = 'cancel' AND metadata ? 'reason'
		  AND changed_at >= now() - make_interval(days => $1)
		GROUP BY 1
		ORDER BY 2 DESC, 1`, days)
	if err != nil {
		return nil, fmt.Errorf("query.cancellation: counts: %w", err)
	}
	for rows.Next() {
		var c ReasonCount
		if err := rows.Scan(&c.Reason, &c.Count); err != nil {
			rows.Close()
			return nil, fmt.Errorf("query.cancellation: counts scan: %w", err)
		}
		r.Counts = append(r.Counts, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query.cancellation: counts: %w", err)
	}

	rows, err = pool.Query(ctx, `
		SELECT metadata->>'reason', metadata->>'details', COALESCE(from_plan, ''), changed_at
		FROM billing.subscription_history
		WHERE action = 'cancel' AND metadata ? 'reason'
		  AND COALESCE(metadata->>'details', '') <> ''
		  AND changed_at >= now() - make_interval(days => $1)
		ORDER BY changed_at DESC
		LIMIT $2`, days, cancellationLatestLimit)
	if err != nil {
		return nil, fmt.Errorf("query.cancellation: latest: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d CancellationDetail
		if err := rows.Scan(&d.Reason, &d.Details, &d.Plan, &d.At); err != nil {
			return nil, fmt.Errorf("query.cancellation: latest scan: %w", err)
		}
		r.Latest = append(r.Latest, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query.cancellation: latest: %w", err)
	}
	return r, nil
}
