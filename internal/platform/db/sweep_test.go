package db_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/aasumitro/stratum/internal/platform/db"
)

// scriptedExec answers each Exec with the next affected-row count, or err once
// they run out.
type scriptedExec struct {
	rows  []string
	err   error
	calls [][]any
}

func (s *scriptedExec) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	s.calls = append(s.calls, args)
	if len(s.rows) == 0 {
		return pgconn.CommandTag{}, s.err
	}
	tag := pgconn.NewCommandTag("DELETE " + s.rows[0])
	s.rows = s.rows[1:]
	return tag, nil
}

func (s *scriptedExec) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("not used")
}

func (s *scriptedExec) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("not used")
}

func TestSweepInBatches_RepeatsUntilAShortBatch(t *testing.T) {
	q := &scriptedExec{rows: []string{"10000", "10000", "1"}}

	total, err := db.SweepInBatches(t.Context(), q, "DELETE", 30)
	if err != nil {
		t.Fatalf("SweepInBatches: %v", err)
	}
	if total != 20001 {
		t.Errorf("total = %d, want 20001", total)
	}
	if len(q.calls) != 3 {
		t.Errorf("ran %d statements, want 3", len(q.calls))
	}
	for _, args := range q.calls {
		if !slices.Equal(args, []any{30, db.SweepBatchSize}) {
			t.Errorf("args = %v, want the caller's args then the batch size", args)
		}
	}
}

func TestSweepInBatches_StopsAfterAnExactlyEmptyLastBatch(t *testing.T) {
	q := &scriptedExec{rows: []string{"10000", "0"}}

	total, err := db.SweepInBatches(t.Context(), q, "DELETE", 30)
	if err != nil {
		t.Fatalf("SweepInBatches: %v", err)
	}
	if total != 10000 || len(q.calls) != 2 {
		t.Errorf("total = %d after %d statements, want 10000 after 2", total, len(q.calls))
	}
}

func TestSweepInBatches_ErrorKeepsTheCountSoFar(t *testing.T) {
	boom := errors.New("boom")
	q := &scriptedExec{rows: []string{"10000"}, err: boom}

	total, err := db.SweepInBatches(t.Context(), q, "DELETE", 30)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
	if total != 10000 {
		t.Errorf("total = %d, want the 10000 already removed", total)
	}
}
