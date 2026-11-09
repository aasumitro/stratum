package audit_test

// Integration tests for the privileges migrations 000011 and 000012 enforce on
// audit.events, run as the real runtime and operator roles. Fixtures go through
// the test role, which owns the schema and is not limited by those privileges.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/audit"
)

const (
	pgInsufficientPrivilege = "42501"
	pgCheckViolation        = "23514"
)

func openRolePool(t *testing.T, env string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skip(env + " not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open %s pool: %v", env, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func wantSQLState(t *testing.T, err error, want string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != want {
		t.Fatalf("want SQLSTATE %s, got %v", want, err)
	}
}

// operatorPool opens the operator login with the application name Studio sets,
// so tests see the same identity columns a real Studio session records.
func operatorPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_OPERATOR_URL")
	if dsn == "" {
		t.Skip("POSTGRES_OPERATOR_URL not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse operator dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "stratum-studio"
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open operator pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestAppendOnly_RuntimeRolesCannotRewriteHistory(t *testing.T) {
	for _, env := range []string{"POSTGRES_APP_URL", "POSTGRES_WORKER_URL", "POSTGRES_WEBHOOK_URL"} {
		t.Run(env, func(t *testing.T) {
			pool := openRolePool(t, env)
			for _, stmt := range []string{
				`UPDATE audit.events SET actor = 'x' WHERE false`,
				`DELETE FROM audit.events WHERE false`,
				`TRUNCATE audit.events`,
			} {
				_, err := pool.Exec(t.Context(), stmt)
				wantSQLState(t, err, pgInsufficientPrivilege)
			}
		})
	}
}

// A table the migration owner adds to the schema later must start out append-only
// too, without anyone remembering to revoke anything.
func TestAppendOnly_FutureAuditTableIsAppendOnlyToo(t *testing.T) {
	pool := testPool(t)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	ctx := t.Context()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE stratum`); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE audit.append_only_probe (id int)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for role, want := range map[string]bool{"stratum_app": false, "stratum_worker": false, "stratum_webhook": false} {
		for _, privilege := range []string{"UPDATE", "DELETE"} {
			var got bool
			if err := tx.QueryRow(ctx, `SELECT has_table_privilege($1, 'audit.append_only_probe', $2)`, role, privilege).Scan(&got); err != nil {
				t.Fatalf("has_table_privilege: %v", err)
			}
			if got != want {
				t.Errorf("%s %s on a new audit table = %v, want %v", role, privilege, got, want)
			}
		}
		var canInsert bool
		if err := tx.QueryRow(ctx, `SELECT has_table_privilege($1, 'audit.append_only_probe', 'INSERT')`, role).Scan(&canInsert); err != nil {
			t.Fatalf("has_table_privilege: %v", err)
		}
		if !canInsert {
			t.Errorf("%s must keep INSERT on a new audit table", role)
		}
	}
}

func TestAppendOnly_SweepFloorIsEnforcedInSQL(t *testing.T) {
	admin := testPool(t)
	app := openRolePool(t, "POSTGRES_APP_URL")
	orgID := "append_only_floor_" + t.Name()
	t.Cleanup(func() {
		admin.Exec(context.Background(), `DELETE FROM audit.events WHERE organization_id = $1`, orgID)
	})

	now := time.Now().UTC()
	seedEvent(t, admin, &orgID, "floor_actor", "POST", "/expired", now.AddDate(0, 0, -366))
	seedEvent(t, admin, &orgID, "floor_actor", "POST", "/kept", now.AddDate(0, 0, -364))

	// A retention of 1 day is what a compromised runtime role would ask for.
	var removed int64
	if err := app.QueryRow(t.Context(), `SELECT audit.sweep_expired(1)`).Scan(&removed); err != nil {
		t.Fatalf("sweep as stratum_app: %v", err)
	}
	if removed < 1 {
		t.Errorf("want the 366-day-old row removed, removed %d", removed)
	}

	rows, err := admin.Query(t.Context(), `SELECT resource FROM audit.events WHERE organization_id = $1`, orgID)
	if err != nil {
		t.Fatalf("read survivors: %v", err)
	}
	survivors, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("read survivors: %v", err)
	}
	if len(survivors) != 1 || survivors[0] != "/kept" {
		t.Errorf("want only /kept to survive, got %v", survivors)
	}
}

func TestAppendOnly_WriterCleanupRunsAsAppRole(t *testing.T) {
	admin := testPool(t)
	app := openRolePool(t, "POSTGRES_APP_URL")
	orgID := "append_only_cleanup_" + t.Name()
	t.Cleanup(func() {
		admin.Exec(context.Background(), `DELETE FROM audit.events WHERE organization_id = $1`, orgID)
	})
	seedEvent(t, admin, &orgID, "cleanup_actor", "POST", "/expired", time.Now().UTC().AddDate(0, 0, -500))

	writer := audit.NewWriter(app, slog.Default(), 400)
	defer writer.Stop()
	audit.RunCleanup(writer)

	var remaining int
	if err := admin.QueryRow(t.Context(),
		`SELECT count(*) FROM audit.events WHERE organization_id = $1`, orgID).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Errorf("want the expired row removed by the app role's cleanup, %d left", remaining)
	}
}

func TestAppendOnly_AnonymizeOnlyFromWorker(t *testing.T) {
	admin := testPool(t)
	worker := openRolePool(t, "POSTGRES_WORKER_URL")
	app := openRolePool(t, "POSTGRES_APP_URL")
	orgID := "append_only_anon_" + t.Name()
	const sub = "append_only_anon_sub"
	t.Cleanup(func() {
		admin.Exec(context.Background(), `DELETE FROM audit.events WHERE organization_id = $1`, orgID)
	})
	seedEvent(t, admin, &orgID, sub, "POST", "/anon", time.Now().UTC())
	if _, err := admin.Exec(t.Context(),
		`UPDATE audit.events SET ip = '203.0.113.9', user_agent = 'test-agent' WHERE organization_id = $1`, orgID); err != nil {
		t.Fatalf("seed ip and user agent: %v", err)
	}

	wantSQLState(t, audit.AnonymizeActor(t.Context(), app, sub), pgInsufficientPrivilege)

	if err := audit.AnonymizeActor(t.Context(), worker, sub); err != nil {
		t.Fatalf("anonymize as stratum_worker: %v", err)
	}
	var actor, ip, userAgent string
	if err := admin.QueryRow(t.Context(),
		`SELECT actor, ip, user_agent FROM audit.events WHERE organization_id = $1`, orgID).Scan(&actor, &ip, &userAgent); err != nil {
		t.Fatalf("read actor: %v", err)
	}
	if actor != "deleted_user" {
		t.Errorf("want actor deleted_user, got %q", actor)
	}
	// Where the request came from and with what client identify the person as
	// much as the actor does.
	if ip != "" || userAgent != "" {
		t.Errorf("want ip and user agent cleared, got %q and %q", ip, userAgent)
	}
}

// Runtime roles insert the application's own rows, and a stolen runtime
// credential must not be able to write one that names an operator.
func TestAppendOnly_RuntimeRolesCannotWriteOperatorRows(t *testing.T) {
	for _, env := range []string{"POSTGRES_APP_URL", "POSTGRES_WORKER_URL", "POSTGRES_WEBHOOK_URL"} {
		t.Run(env, func(t *testing.T) {
			pool := openRolePool(t, env)
			_, err := pool.Exec(t.Context(), `
				INSERT INTO audit.events (actor, action, resource, status_code)
				VALUES ('operator:forged', 'studio.billing.invoices.update', 'billing.invoices/x', 200)`)
			wantSQLState(t, err, pgInsufficientPrivilege)
		})
	}
}

// Who an operator was and the record of unauthenticated probing are what this log
// is for, and no deleted customer's identity ever takes these forms.
func TestAppendOnly_ProtectedActorsCannotBeAnonymized(t *testing.T) {
	admin := testPool(t)
	worker := openRolePool(t, "POSTGRES_WORKER_URL")
	for _, actor := range []string{"operator:op_append_only_test", "anonymous", "deleted_user"} {
		t.Run(actor, func(t *testing.T) {
			wantSQLState(t, audit.AnonymizeActor(t.Context(), worker, actor), pgInsufficientPrivilege)
		})
	}

	orgID := "append_only_operator_" + t.Name()
	t.Cleanup(func() {
		admin.Exec(context.Background(), `DELETE FROM audit.events WHERE organization_id = $1`, orgID)
	})
	const actor = "operator:op_append_only_test"
	seedEvent(t, admin, &orgID, actor, "studio.dlq.purge", "/operator", time.Now().UTC())
	if err := audit.AnonymizeActor(t.Context(), worker, actor); err == nil {
		t.Fatal("want refusal")
	}
	var got string
	if err := admin.QueryRow(t.Context(),
		`SELECT actor FROM audit.events WHERE organization_id = $1`, orgID).Scan(&got); err != nil {
		t.Fatalf("read actor: %v", err)
	}
	if got != actor {
		t.Errorf("operator attribution changed to %q", got)
	}
}

func TestAppendOnly_WriterCleanupDrainsPastOneBatch(t *testing.T) {
	admin := testPool(t)
	app := openRolePool(t, "POSTGRES_APP_URL")
	orgID := "append_only_drain_" + t.Name()
	t.Cleanup(func() {
		admin.Exec(context.Background(), `DELETE FROM audit.events WHERE organization_id = $1`, orgID)
	})

	// One past the function's per-call limit.
	if _, err := admin.Exec(t.Context(), `
		INSERT INTO audit.events (organization_id, actor, action, resource, status_code, created_at)
		SELECT $1, 'drain_actor', 'POST', '/drain', 200, now() - interval '500 days' - make_interval(secs => g)
		FROM generate_series(1, 50001) AS g`, orgID); err != nil {
		t.Fatalf("seed: %v", err)
	}

	writer := audit.NewWriter(app, slog.Default(), 400)
	defer writer.Stop()
	audit.RunCleanup(writer)

	var left int
	if err := admin.QueryRow(t.Context(),
		`SELECT count(*) FROM audit.events WHERE organization_id = $1`, orgID).Scan(&left); err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 0 {
		t.Errorf("%d rows left, want one cleanup to drain more than one batch", left)
	}
}

func TestOperator_IsOperatorMeansMembershipNotSuperuser(t *testing.T) {
	pool := testPool(t)
	for role, want := range map[string]bool{
		"stratum_operator_dev": true,
		"stratum":              false, // a superuser here: pg_has_role would say true
		"stratum_app":          false,
		"stratum_test":         false,
		"stratum_operator":     false, // the group itself cannot log in
	} {
		var got bool
		if err := pool.QueryRow(t.Context(), `SELECT audit.is_operator($1)`, role).Scan(&got); err != nil {
			t.Fatalf("is_operator(%s): %v", role, err)
		}
		if got != want {
			t.Errorf("is_operator(%s): want %v, got %v", role, want, got)
		}
	}
}

// A login that reaches the group through another group inherits its write privileges,
// so it must not escape capture. Roles are transactional, so nothing outlives the test.
func TestOperator_IsOperatorFollowsNestedGrants(t *testing.T) {
	pool := testPool(t)
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	for _, stmt := range []string{
		`SET LOCAL ROLE stratum`,
		`CREATE ROLE nested_ops_team NOLOGIN IN ROLE stratum_operator`,
		`CREATE ROLE nested_op_alice LOGIN IN ROLE nested_ops_team`,
		`CREATE ROLE unrelated_login LOGIN`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	for role, want := range map[string]bool{"nested_op_alice": true, "unrelated_login": false} {
		var got bool
		if err := tx.QueryRow(ctx, `SELECT audit.is_operator($1)`, role).Scan(&got); err != nil {
			t.Fatalf("is_operator(%s): %v", role, err)
		}
		if got != want {
			t.Errorf("is_operator(%s): want %v, got %v", role, want, got)
		}
	}
}

func TestOperator_CaptureTriggerCoversEveryWritableTable(t *testing.T) {
	pool := testPool(t)
	rows, err := pool.Query(t.Context(), `
		SELECT c.oid::regclass::text
		FROM pg_trigger t
		JOIN pg_class c ON c.oid = t.tgrelid
		WHERE t.tgname = 'audit_operator_change'
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("list triggers: %v", err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("list triggers: %v", err)
	}
	want := []string{
		"billing.addon_features", "billing.addons", "billing.coupon_redemptions", "billing.coupon_targets",
		"billing.coupons", "billing.features", "billing.invoices", "billing.payment_links", "billing.payments",
		"billing.plan_features",
		"billing.plans", "billing.subscriptions", "messaging.outbox", "organization.organizations",
		"ref.countries", "ref.currencies",
	}
	if len(got) != len(want) {
		t.Fatalf("want triggers on %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want triggers on %v, got %v", want, got)
		}
	}
}

func TestOperator_ChangeIsAttributedWithOldAndNewRows(t *testing.T) {
	admin := testPool(t)
	operator := operatorPool(t)
	const code = "AUDIT_CAPTURE_TEST"
	t.Cleanup(func() {
		ctx := context.Background()
		admin.Exec(ctx, `DELETE FROM billing.coupons WHERE code = $1`, code)
		admin.Exec(ctx, `DELETE FROM audit.events WHERE resource = $1`, "billing.coupons/"+code)
	})

	if _, err := operator.Exec(t.Context(), `
		INSERT INTO billing.coupons (code, name, discount_type, percent_off) VALUES ($1, 'before', 'percent', 10)`, code); err != nil {
		t.Fatalf("insert as operator: %v", err)
	}
	if _, err := operator.Exec(t.Context(), `UPDATE billing.coupons SET name = 'after' WHERE code = $1`, code); err != nil {
		t.Fatalf("update as operator: %v", err)
	}
	if _, err := operator.Exec(t.Context(), `DELETE FROM billing.coupons WHERE code = $1`, code); err != nil {
		t.Fatalf("delete as operator: %v", err)
	}

	type capturedRow struct {
		Old *struct {
			Name string `json:"name"`
		} `json:"old"`
		New *struct {
			Name string `json:"name"`
		} `json:"new"`
	}
	check := func(action string, verify func(capturedRow)) {
		t.Helper()
		var actor, userAgent string
		var orgID *string
		var status int
		var metadata []byte
		err := admin.QueryRow(t.Context(), `
			SELECT actor, organization_id, status_code, metadata::text, user_agent
			FROM audit.events WHERE action = $1 AND resource = $2`,
			action, "billing.coupons/"+code,
		).Scan(&actor, &orgID, &status, &metadata, &userAgent)
		if err != nil {
			t.Fatalf("%s: read audit row: %v", action, err)
		}
		if actor != "operator:stratum_operator_dev" {
			t.Errorf("%s: actor = %q", action, actor)
		}
		if orgID != nil {
			t.Errorf("%s: organization_id = %v, want NULL", action, *orgID)
		}
		if status != 200 {
			t.Errorf("%s: status_code = %d", action, status)
		}
		if userAgent != "stratum-studio" {
			t.Errorf("%s: user_agent = %q", action, userAgent)
		}
		var row capturedRow
		if err := json.Unmarshal(metadata, &row); err != nil {
			t.Fatalf("%s: decode metadata: %v", action, err)
		}
		verify(row)
	}
	check("studio.billing.coupons.insert", func(r capturedRow) {
		if r.Old != nil || r.New == nil || r.New.Name != "before" {
			t.Errorf("insert metadata = %+v", r)
		}
	})
	check("studio.billing.coupons.update", func(r capturedRow) {
		if r.Old == nil || r.Old.Name != "before" || r.New == nil || r.New.Name != "after" {
			t.Errorf("update metadata = %+v", r)
		}
	})
	check("studio.billing.coupons.delete", func(r capturedRow) {
		if r.Old == nil || r.Old.Name != "after" || r.New != nil {
			t.Errorf("delete metadata = %+v", r)
		}
	})
}

// The API updates a subscription through the RLS-constrained role, inside a
// transaction scoped to its organization, on a table the trigger watches.
func TestOperator_APIRoleUpdatingASubscriptionIsNotCaptured(t *testing.T) {
	admin := testPool(t)
	app := openRolePool(t, "POSTGRES_APP_URL")
	const orgID = "0e1e0000-0000-0000-0000-0000000000a5"

	var subscription string
	if err := admin.QueryRow(t.Context(), `
		INSERT INTO billing.subscriptions (subject_type, subject_id, status) VALUES ('organization', $1, 'active') RETURNING id`,
		orgID).Scan(&subscription); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), `DELETE FROM billing.subscriptions WHERE id = $1`, subscription)
	})

	tx, err := app.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), `SELECT set_config('app.organization_id', $1, true)`, orgID); err != nil {
		t.Fatalf("scope transaction: %v", err)
	}
	tag, err := tx.Exec(t.Context(), `UPDATE billing.subscriptions SET cycle = 'yearly' WHERE id = $1`, subscription)
	if err != nil {
		t.Fatalf("update as stratum_app: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("updated %d rows, want the seeded one", tag.RowsAffected())
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var rows int
	if err := admin.QueryRow(t.Context(),
		`SELECT count(*) FROM audit.events WHERE resource = $1`, "billing.subscriptions/"+subscription).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 0 {
		t.Errorf("want no audit row for an application write, got %d", rows)
	}
}

// These values work as a credential or name a person, and the audit trail outlives
// the row they came from.
func TestOperator_CaptureRemovesCredentialsAndIdentifiers(t *testing.T) {
	admin := testPool(t)
	operator := operatorPool(t)
	const (
		slug       = "audit-capture-removal"
		inviteCode = "AUDITCAPTURESECRET"
		ownerSub   = "audit_capture_owner_sub"
	)
	var orgID string
	if err := admin.QueryRow(t.Context(), `
		INSERT INTO organization.organizations (slug, name, owner_id, invite_code) VALUES ($1, 'Capture', $2, $3) RETURNING id`,
		slug, ownerSub, inviteCode).Scan(&orgID); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		admin.Exec(ctx, `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		admin.Exec(ctx, `DELETE FROM audit.events WHERE resource = $1`, "organization.organizations/"+orgID)
	})

	if _, err := operator.Exec(t.Context(),
		`UPDATE organization.organizations SET status = 'suspended' WHERE id = $1`, orgID); err != nil {
		t.Fatalf("update as operator: %v", err)
	}

	var metadata string
	if err := admin.QueryRow(t.Context(), `
		SELECT metadata::text FROM audit.events WHERE resource = $1`,
		"organization.organizations/"+orgID).Scan(&metadata); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	for _, leaked := range []string{inviteCode, ownerSub, `"invite_code"`, `"owner_id"`} {
		if strings.Contains(metadata, leaked) {
			t.Errorf("metadata contains %q: %s", leaked, metadata)
		}
	}
	if !strings.Contains(metadata, `"status": "suspended"`) {
		t.Errorf("the change itself must stay recorded: %s", metadata)
	}
}

// A coupon aimed at one person names them by auth_sub, which the audit trail outlives.
// A target that is an organization stays readable: it is not personal data.
func TestOperator_CaptureRemovesAPersonNamedAsACouponTarget(t *testing.T) {
	admin := testPool(t)
	operator := operatorPool(t)
	const (
		code     = "AUDITCAPTURETARGET"
		person   = "audit_capture_target_sub"
		orgID    = "0e1e0000-0000-0000-0000-0000000000a6"
		resource = "billing.coupon_targets/" + code
	)
	if _, err := admin.Exec(t.Context(), `
		INSERT INTO billing.coupons (code, name, discount_type, amount_cents, currency)
		VALUES ($1, 'Capture', 'fixed', 100, 'USD')`, code); err != nil {
		t.Fatalf("seed coupon: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		admin.Exec(ctx, `DELETE FROM billing.coupon_targets WHERE coupon_id = $1`, code)
		admin.Exec(ctx, `DELETE FROM billing.coupons WHERE code = $1`, code)
		admin.Exec(ctx, `DELETE FROM audit.events WHERE resource = $1`, resource)
	})

	for subjectType, subjectID := range map[string]string{"user": person, "organization": orgID} {
		if _, err := operator.Exec(t.Context(), `
			INSERT INTO billing.coupon_targets (coupon_id, subject_type, subject_id) VALUES ($1, $2, $3)`,
			code, subjectType, subjectID); err != nil {
			t.Fatalf("add %s target: %v", subjectType, err)
		}
	}

	var rows int
	var metadata string
	if err := admin.QueryRow(t.Context(), `
		SELECT count(*), string_agg(metadata::text, ' ') FROM audit.events WHERE resource = $1`,
		resource).Scan(&rows, &metadata); err != nil {
		t.Fatalf("read audit rows: %v", err)
	}
	if rows != 2 {
		t.Errorf("want two audit rows, got %d", rows)
	}
	if strings.Contains(metadata, person) {
		t.Errorf("metadata names the person: %s", metadata)
	}
	if !strings.Contains(metadata, orgID) {
		t.Errorf("the organization target must stay recorded: %s", metadata)
	}
}

func TestOperator_ApplicationWritesAreNotCaptured(t *testing.T) {
	admin := testPool(t)
	worker := openRolePool(t, "POSTGRES_WORKER_URL")
	const code = "AUDIT_NOT_CAPTURED_TEST"
	t.Cleanup(func() {
		ctx := context.Background()
		admin.Exec(ctx, `DELETE FROM billing.coupons WHERE code = $1`, code)
	})

	if _, err := worker.Exec(t.Context(), `
		INSERT INTO billing.coupons (code, name, discount_type, percent_off) VALUES ($1, 'app', 'percent', 10)`, code); err != nil {
		t.Fatalf("insert as worker: %v", err)
	}
	if _, err := worker.Exec(t.Context(), `UPDATE billing.coupons SET name = 'app2' WHERE code = $1`, code); err != nil {
		t.Fatalf("update as worker: %v", err)
	}

	var n int
	if err := admin.QueryRow(t.Context(),
		`SELECT count(*) FROM audit.events WHERE resource = $1`, "billing.coupons/"+code).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("want no audit row for an application write, got %d", n)
	}
}

func TestOperator_RecordAction(t *testing.T) {
	admin := testPool(t)
	operator := operatorPool(t)
	app := openRolePool(t, "POSTGRES_APP_URL")
	const resource = "audit-record-action-test"
	t.Cleanup(func() {
		admin.Exec(context.Background(), `DELETE FROM audit.events WHERE resource = $1`, resource)
	})
	const record = `SELECT audit.record_operator_action($1, $2, $3)`

	t.Run("a role without EXECUTE is refused", func(t *testing.T) {
		_, err := app.Exec(t.Context(), record, "studio.dlq.purge", resource, `{}`)
		wantSQLState(t, err, pgInsufficientPrivilege)
	})
	t.Run("a schema owner is not an operator", func(t *testing.T) {
		_, err := admin.Exec(t.Context(), record, "studio.dlq.purge", resource, `{}`)
		wantSQLState(t, err, pgInsufficientPrivilege)
	})
	t.Run("an action outside the namespace is refused", func(t *testing.T) {
		_, err := operator.Exec(t.Context(), record, "dlq.purge", resource, `{}`)
		wantSQLState(t, err, pgCheckViolation)
	})
	t.Run("an operator action is recorded", func(t *testing.T) {
		if _, err := operator.Exec(t.Context(), record, "studio.dlq.purge", resource, `{"count": 3}`); err != nil {
			t.Fatalf("record: %v", err)
		}
		var actor, action string
		var count int
		err := admin.QueryRow(t.Context(), `
			SELECT actor, action, (metadata->>'count')::int FROM audit.events WHERE resource = $1`, resource,
		).Scan(&actor, &action, &count)
		if err != nil {
			t.Fatalf("read row: %v", err)
		}
		if actor != "operator:stratum_operator_dev" || action != "studio.dlq.purge" || count != 3 {
			t.Errorf("got actor=%q action=%q count=%d", actor, action, count)
		}
	})
}

func TestOperator_GrantsStopAtTheWriteList(t *testing.T) {
	operator := operatorPool(t)
	for _, stmt := range []string{
		`UPDATE audit.events SET actor = 'x' WHERE false`,
		`DELETE FROM audit.events WHERE false`,
		`INSERT INTO audit.events (actor, action, resource, status_code) VALUES ('x', 'x', 'x', 200)`,
		`UPDATE account.users SET full_name = 'x' WHERE false`,
		`UPDATE organization.webhook_endpoints SET enabled = false WHERE false`,
		`DELETE FROM organization.organizations WHERE false`,
		`UPDATE billing.payments SET amount_cents = 1 WHERE false`,
		`UPDATE billing.coupon_redemptions SET coupon_id = 'x' WHERE false`,
		`SELECT audit.sweep_expired(400)`,
		`SELECT audit.anonymize_actor('x')`,
	} {
		_, err := operator.Exec(t.Context(), stmt)
		wantSQLState(t, err, pgInsufficientPrivilege)
	}
}

// An unauthenticated caller controls the user agent and the request body. A value
// Postgres refuses must cost that one row at most: a batch is one statement, so
// otherwise a few such requests per second would erase everyone else's records.
func TestWriter_ValuesPostgresRefusesCostOnlyTheirOwnRow(t *testing.T) {
	admin := testPool(t)
	app := openRolePool(t, "POSTGRES_APP_URL")
	const resource = "/audit-poison-test"
	t.Cleanup(func() {
		admin.Exec(context.Background(), `DELETE FROM audit.events WHERE resource = $1`, resource)
	})

	writer := audit.NewWriter(app, slog.Default(), 400)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(writer.Middleware())
	engine.POST(resource, func(c *gin.Context) { c.Status(http.StatusUnauthorized) })

	post := func(userAgent, body string) {
		req := httptest.NewRequest(http.MethodPost, resource, strings.NewReader(body))
		req.Header.Set("User-Agent", userAgent)
		engine.ServeHTTP(httptest.NewRecorder(), req)
	}
	nesting := strings.Repeat("[", 2000) + strings.Repeat("]", 2000)
	post("clean-before", `{"who":"clean-before"}`)
	post("\xff\xfe", `{"who":"bad-user-agent"}`)
	post("nul-escape", `{"a":"\u0000"}`)
	post("lone-surrogate", `{"a":"\ud800"}`)
	post("numeric-overflow", `{"a":1e999999}`)
	post("deep-nesting", nesting)
	post("nul-in-agent\x00", `{"who":"nul-in-agent"}`)
	post("clean-after", `{"who":"clean-after"}`)
	writer.Stop()

	var total int
	if err := admin.QueryRow(t.Context(), `SELECT count(*) FROM audit.events WHERE resource = $1`, resource).Scan(&total); err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 8 {
		t.Errorf("stored %d of 8 rows", total)
	}

	for _, agent := range []string{"clean-before", "clean-after", "nul-in-agent"} {
		var who string
		if err := admin.QueryRow(t.Context(), `
			SELECT metadata->>'who' FROM audit.events WHERE resource = $1 AND user_agent = $2`,
			resource, agent).Scan(&who); err != nil {
			t.Errorf("%s: read row: %v", agent, err)
			continue
		}
		if who != agent {
			t.Errorf("%s: metadata lost, got %q", agent, who)
		}
	}

	// The refused value goes, and the record of the request stays.
	var who string
	if err := admin.QueryRow(t.Context(), `
		SELECT metadata->>'who' FROM audit.events WHERE resource = $1 AND user_agent = $2`,
		resource, "�").Scan(&who); err != nil {
		t.Errorf("bad user agent: read row: %v", err)
	} else if who != "bad-user-agent" {
		t.Errorf("bad user agent: metadata = %q", who)
	}
	for _, agent := range []string{"nul-escape", "numeric-overflow"} {
		var metadata string
		if err := admin.QueryRow(t.Context(), `
			SELECT metadata::text FROM audit.events WHERE resource = $1 AND user_agent = $2`,
			resource, agent).Scan(&metadata); err != nil {
			t.Errorf("%s: the request's record must stay: %v", agent, err)
			continue
		}
		if metadata != "{}" {
			t.Errorf("%s: metadata = %s, want {}", agent, metadata)
		}
	}
}
