package db_test

import (
	"testing"

	"github.com/aasumitro/stratum/internal/platform/db"
)

func TestRequireTx_PanicsAgainstBarePool(t *testing.T) {
	pool := testPool(t)

	defer func() {
		if r := recover(); r == nil {
			t.Error("RequireTx should panic given the bare pool, recovered nothing")
		}
	}()
	db.RequireTx(pool)
	t.Error("RequireTx should have panicked before reaching this line")
}

func TestRequireTx_DoesNotPanicAgainstRealTx(t *testing.T) {
	pool := testPool(t)
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("pool.Begin: %v", err)
	}
	defer tx.Rollback(ctx)

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RequireTx should not panic given a real pgx.Tx, panicked: %v", r)
		}
	}()
	db.RequireTx(tx)
}
