package db

import (
	"context"
	"fmt"
)

// SweepBatchSize bounds one retention statement, so a large backlog goes in many
// short transactions rather than one long one holding locks.
const SweepBatchSize = 10000

// SweepInBatches runs a retention statement until it touches less than a full
// batch, and returns how many rows it touched. query receives args followed by
// SweepBatchSize as its last parameter, and must stop selecting a row once it has
// handled it, or this never ends. On a pool each run commits on its own, so an
// error keeps what the earlier runs did and reports it in the returned count.
func SweepInBatches(ctx context.Context, q Querier, query string, args ...any) (int64, error) {
	args = append(args, SweepBatchSize)
	var total int64
	for {
		tag, err := q.Exec(ctx, query, args...)
		if err != nil {
			return total, fmt.Errorf("db.SweepInBatches: %w", err)
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < SweepBatchSize {
			return total, nil
		}
	}
}
