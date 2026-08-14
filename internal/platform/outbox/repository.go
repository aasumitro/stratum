// Package outbox implements both sides of the transactional outbox pattern:
// Enqueue/EnqueueDelayed write a row inside the caller's own transaction,
// and the relay (see relay.go) later claims, delivers, and marks rows
// published. Imports only db and messaging, matching platform's "never
// imports modules or contracts" rule — callers (events.Enqueue, in
// internal/contracts/events) construct and marshal the envelope themselves
// and pass this package only primitives, never the Envelope type itself, to
// keep that boundary intact.
package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Row is one claimed messaging.outbox record — just enough for the relay to
// hand off to a publisher and know how many times it's already been tried.
type Row struct {
	ID         string
	Exchange   string
	RoutingKey string
	Payload    []byte
	Attempts   int
}

// Enqueue inserts one messaging.outbox row using q — the caller (events.Enqueue)
// is responsible for q being the same transaction as the state change the
// event describes, so both commit or neither does. A failure here must
// propagate to the caller's transaction: a row that silently fails to
// insert reopens the exact fire-and-forget gap this package exists to
// close.
func Enqueue(ctx context.Context, q db.Querier, id, exchange, routingKey string, payload []byte) error {
	_, err := q.Exec(ctx,
		`INSERT INTO messaging.outbox (id, exchange, routing_key, payload) VALUES ($1, $2, $3, $4)`,
		id, exchange, routingKey, payload)
	if err != nil {
		return fmt.Errorf("outbox.Enqueue: insert outbox row: %w", err)
	}
	return nil
}

// EnqueueDelayed is Enqueue with not_before set delay in the future — the
// outbox analogue of a delayed publish, unifying "publish now" and "publish
// later" into the relay's single not_before <= now() query instead of a
// separate delayed-outbox mechanism.
func EnqueueDelayed(ctx context.Context, q db.Querier, id, exchange, routingKey string, payload []byte, delay time.Duration) error {
	_, err := q.Exec(ctx,
		`INSERT INTO messaging.outbox (id, exchange, routing_key, payload, not_before) VALUES ($1, $2, $3, $4, now() + $5)`,
		id, exchange, routingKey, payload, delay)
	if err != nil {
		return fmt.Errorf("outbox.EnqueueDelayed: insert outbox row: %w", err)
	}
	return nil
}

// claimUnpublished locks and returns up to limit due, unpublished rows. q
// must be a transaction that the caller keeps open until every claimed row
// is marked published or failed — FOR UPDATE SKIP LOCKED's row locks are
// held only for the lifetime of that transaction, not the query itself; run
// against a bare pool connection (no BEGIN) they release the instant this
// query completes, before a second concurrent caller could ever be blocked
// by them. Held for the whole claim-to-mark window, they're what let
// cmd/worker scale to more than one replica without both claiming the same
// row.
func claimUnpublished(ctx context.Context, q db.Querier, limit, maxAttempts int) ([]Row, error) {
	rows, err := q.Query(ctx, `
		SELECT id, exchange, routing_key, payload, attempts
		FROM messaging.outbox
		WHERE published_at IS NULL AND not_before <= now() AND attempts < $2
		ORDER BY created_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED`,
		limit, maxAttempts,
	)
	if err != nil {
		return nil, fmt.Errorf("outbox.claimUnpublished: %w", err)
	}
	defer rows.Close()

	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.ID, &r.Exchange, &r.RoutingKey, &r.Payload, &r.Attempts); err != nil {
			return nil, fmt.Errorf("outbox.claimUnpublished: scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox.claimUnpublished: %w", err)
	}
	return out, nil
}

// markPublished marks id as delivered — the relay never claims it again.
func markPublished(ctx context.Context, q db.Querier, id string) error {
	_, err := q.Exec(ctx, `UPDATE messaging.outbox SET published_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("outbox.markPublished: %w", err)
	}
	return nil
}

// recordFailure increments attempts and records the error for operator
// visibility. published_at stays NULL, so the row is picked up again by the
// next RelayBatch run. not_before is pushed forward by an exponential
// backoff proportional to attempts, capped at the least(attempts, 10) exponent.
func recordFailure(ctx context.Context, q db.Querier, id string, publishErr error) error {
	_, err := q.Exec(ctx, `
		UPDATE messaging.outbox
		SET attempts = attempts + 1,
		    last_error = $2,
		    not_before = now() + (interval '2 seconds' * power(2, least(attempts, 10)))
		WHERE id = $1`,
		id, publishErr.Error(),
	)
	if err != nil {
		return fmt.Errorf("outbox.recordFailure: %w", err)
	}
	return nil
}

// Sweep permanently deletes published messaging.outbox rows older than
// retentionDays, computed as time.Now().AddDate(0, 0, -retentionDays) —
// matching audit.Writer.cleanup's cutoff shape, not a SQL-side interval.
// Unpublished rows (still in flight, or exhausted past maxAttempts) are
// never touched by this query regardless of age.
func Sweep(ctx context.Context, pool *pgxpool.Pool, retentionDays int) error {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	_, err := pool.Exec(ctx,
		`DELETE FROM messaging.outbox WHERE published_at IS NOT NULL AND published_at < $1`,
		cutoff,
	)
	if err != nil {
		return fmt.Errorf("outbox.Sweep: %w", err)
	}
	return nil
}
