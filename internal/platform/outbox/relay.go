package outbox

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/db"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// batchSize caps how many rows one RelayBatch run claims — bounds worst-case
// per-tick work so a large backlog drains over several ticks instead of one
// unbounded batch.
const batchSize = 100

// maxAttempts is the ceiling on outbox row delivery attempts: claimUnpublished
// excludes rows at or beyond this count, so a row that can never publish (a
// malformed payload, a permanently-unroutable key) stops competing for claim
// slots against every other due row instead of stalling the relay.
const maxAttempts = 20

// Relay delivers due, unpublished messaging.outbox rows to the broker. One
// Relay is constructed per cmd/worker process and driven by a ticker (see
// internal/app/worker.go's RunWorker).
type Relay struct {
	pool *pgxpool.Pool
	pub  messaging.EventPublisher
}

// NewRelay constructs a Relay backed by pool (for claiming/marking rows) and
// pub (for the actual broker delivery).
func NewRelay(pool *pgxpool.Pool, pub messaging.EventPublisher) *Relay {
	return &Relay{pool: pool, pub: pub}
}

// RelayBatch claims up to batchSize due rows and publishes each: a publish
// failure records the attempt (visible via attempts/last_error) and leaves
// the row for the next run to retry; a publish success marks the row
// published. The claim, every publish, and every mark/record run inside one
// transaction, so the claim's row locks actually stay held for the whole
// window instead of releasing the instant the claim query completes — the
// real requirement for FOR UPDATE SKIP LOCKED to do anything across two
// concurrent cmd/worker replicas. Errors are logged, not returned —
// RelayBatch is driven by a ticker with no caller to propagate a failure to.
func (r *Relay) RelayBatch(ctx context.Context) {
	err := db.WithTx(ctx, r.pool, func(tx db.Querier) error {
		rows, err := claimUnpublished(ctx, tx, batchSize, maxAttempts)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := r.pub.Publish(ctx, row.Exchange, row.RoutingKey, row.Payload); err != nil {
				_ = recordFailure(ctx, tx, row.ID, err)
				slog.WarnContext(ctx, "outbox: publish failed, will retry", "id", row.ID, "attempts", row.Attempts+1, "error", err)
				continue
			}
			if err := markPublished(ctx, tx, row.ID); err != nil {
				slog.ErrorContext(ctx, "outbox: publish succeeded but mark-published failed — row will republish (safe: consumers are idempotent)", "id", row.ID, "error", err)
			}
		}
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "outbox: claim batch failed", "error", err)
	}
}
