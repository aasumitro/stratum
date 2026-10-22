package db_test

import (
	"context"
	"errors"
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

// TestWithOrgTxAndWithAuthTx_SetTheirGUCOnlyInsideTheTransaction pins the
// contract organization/notification RLS relies on: inside fn the GUC holds
// the argument (and fn's ctx carries the transaction); once the transaction
// ends the setting reads back empty, so a pooled connection never leaks one
// operation's scope into the next.
func TestWithOrgTxAndWithAuthTx_SetTheirGUCOnlyInsideTheTransaction(t *testing.T) {
	pool := testPool(t)
	ctx := t.Context()
	const orgID = "0e0e0000-0000-0000-0000-00000000aaaa"

	cases := []struct {
		name, guc, want string
		run             func(fn func(context.Context, db.Querier) error) error
	}{
		{"org", "app.organization_id", orgID, func(fn func(context.Context, db.Querier) error) error {
			return db.WithOrgTx(ctx, pool, orgID, fn)
		}},
		{"auth", "app.auth_sub", "user_abc", func(fn func(context.Context, db.Querier) error) error {
			return db.WithAuthTx(ctx, pool, "user_abc", fn)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var inside string
			err := tc.run(func(txCtx context.Context, q db.Querier) error {
				if !db.HasQuerier(txCtx) {
					t.Error("fn's ctx should carry the transaction")
				}
				return q.QueryRow(txCtx, "SELECT current_setting($1, true)", tc.guc).Scan(&inside)
			})
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if inside != tc.want {
				t.Errorf("inside the transaction: want %s = %q, got %q", tc.guc, tc.want, inside)
			}
			var after *string
			if err := pool.QueryRow(ctx, "SELECT NULLIF(current_setting($1, true), '')", tc.guc).Scan(&after); err != nil {
				t.Fatalf("read after: %v", err)
			}
			if after != nil {
				t.Errorf("after the transaction: want %s unset or empty, got %q", tc.guc, *after)
			}
		})
	}
}

// TestWithOrgTx_RefusesAnotherOrgUnderAnAuthorizedScope covers the scope
// binding: once a request is authorized for one organization, an operation
// scoped to a different one fails before any connection is taken.
func TestWithOrgTx_RefusesAnotherOrgUnderAnAuthorizedScope(t *testing.T) {
	pool := testPool(t)
	ctx := db.WithAuthorizedOrg(t.Context(), "0e0e0000-0000-0000-0000-00000000aaaa")

	called := false
	err := db.WithOrgTx(ctx, pool, "0e0e0000-0000-0000-0000-00000000bbbb", func(context.Context, db.Querier) error {
		called = true
		return nil
	})
	if !errors.Is(err, db.ErrOrgScopeMismatch) {
		t.Fatalf("want ErrOrgScopeMismatch, got %v", err)
	}
	if called {
		t.Error("fn must not run for a mismatched organization")
	}
	if err := db.WithOrgTx(ctx, pool, "0e0e0000-0000-0000-0000-00000000aaaa", func(context.Context, db.Querier) error {
		return nil
	}); err != nil {
		t.Errorf("the authorized organization itself: want nil, got %v", err)
	}
}
