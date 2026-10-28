package organization

// Integration tests for the organization schema's row-level security
// (migration 000010) and for the module paths that must keep working under
// it. They connect as the RLS-constrained runtime role (POSTGRES_APP_URL)
// and the unscoped BYPASSRLS role (POSTGRES_UNSCOPED_URL); fixtures are
// seeded and removed through the BYPASSRLS test role (TEST_DATABASE_URL).

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/db"
)

const (
	rlsOrgA   = "0e1e0000-0000-0000-0000-0000000000a1"
	rlsOrgB   = "0e1e0000-0000-0000-0000-0000000000b1"
	rlsAlice  = "rls_alice" // owner of A
	rlsCarol  = "rls_carol" // member of A
	rlsBob    = "rls_bob"   // owner of B
	rlsSlugC  = "rlstest-c" // created through createOrganization
	rlsHookID = "rls-hook"
)

type rlsPools struct{ admin, app, unscoped *pgxpool.Pool }

func openRLSPool(t *testing.T, env string, maxConns int32) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skip(env + " not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse %s: %v", env, err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open %s pool: %v", env, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func cleanupRLSFixtures(admin *pgxpool.Pool) {
	ctx := context.Background()
	admin.Exec(ctx, `DELETE FROM messaging.outbox WHERE payload->>'org_id' IN ($1, $2) OR payload->>'organization_id' IN ($1, $2)
		OR payload->'data'->>'organization_id' IN ($1, $2)
		OR payload->'data'->>'organization_id' IN (SELECT id::text FROM organization.organizations WHERE slug = $3)`,
		rlsOrgA, rlsOrgB, rlsSlugC)
	admin.Exec(ctx, `DELETE FROM organization.organizations WHERE id IN ($1, $2) OR slug = $3`, rlsOrgA, rlsOrgB, rlsSlugC)
}

// seedRLSFixtures creates two organizations with one row in every
// organization table each, so every policy has a foreign-tenant row to hide.
func seedRLSFixtures(t *testing.T) rlsPools {
	t.Helper()
	p := rlsPools{
		admin:    openRLSPool(t, "TEST_DATABASE_URL", 0),
		app:      openRLSPool(t, "POSTGRES_APP_URL", 0),
		unscoped: openRLSPool(t, "POSTGRES_UNSCOPED_URL", 0),
	}
	cleanupRLSFixtures(p.admin)
	t.Cleanup(func() { cleanupRLSFixtures(p.admin) })

	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := p.admin.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO organization.organizations (id, slug, name, owner_id) VALUES
		($1, 'rlstest-a', 'RLS A', $3), ($2, 'rlstest-b', 'RLS B', $4)`, rlsOrgA, rlsOrgB, rlsAlice, rlsBob)
	exec(`INSERT INTO organization.memberships (organization_id, auth_sub, role) VALUES
		($1, $3, 'owner'), ($1, $4, 'member'), ($2, $5, 'owner')`, rlsOrgA, rlsOrgB, rlsAlice, rlsCarol, rlsBob)
	exec(`INSERT INTO organization.invitations (organization_id, email, role, token, invited_by, expires_at) VALUES
		($1, 'rls-a@example.com', 'member', 'rls-token-a', $3, now() + interval '1 day'),
		($2, 'rls-b@example.com', 'member', 'rls-token-b', $4, now() + interval '1 day')`, rlsOrgA, rlsOrgB, rlsAlice, rlsBob)
	repo := &repository{}
	for _, org := range []string{rlsOrgA, rlsOrgB} {
		ep, err := repo.insertWebhookEndpoint(ctx, p.admin, org, "https://example.com/"+rlsHookID, "secret", nil, 1, testSecretEncryptionKey, "")
		if err != nil {
			t.Fatalf("seed webhook endpoint: %v", err)
		}
		exec(`INSERT INTO organization.webhook_deliveries (endpoint_id, event_id, event_type) VALUES ($1, 'rls-evt', 'test')`, ep.ID)
	}
	return p
}

// orgTableCounts returns how many fixture rows of each organization table
// q can see, for both tenants.
func orgTableCounts(ctx context.Context, t *testing.T, q db.Querier, org string) map[string]int {
	t.Helper()
	queries := map[string]string{
		"organizations":      `SELECT count(*) FROM organization.organizations WHERE id = $1`,
		"memberships":        `SELECT count(*) FROM organization.memberships WHERE organization_id = $1`,
		"invitations":        `SELECT count(*) FROM organization.invitations WHERE organization_id = $1`,
		"webhook_endpoints":  `SELECT count(*) FROM organization.webhook_endpoints WHERE organization_id = $1`,
		"webhook_deliveries": `SELECT count(*) FROM organization.webhook_deliveries d JOIN organization.webhook_endpoints e ON e.id = d.endpoint_id WHERE e.organization_id = $1`,
	}
	out := map[string]int{}
	for table, sql := range queries {
		var n int
		if err := q.QueryRow(ctx, sql, org).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		out[table] = n
	}
	return out
}

func TestRLS_OrganizationTables_IsolateTenants(t *testing.T) {
	p := seedRLSFixtures(t)
	ctx := t.Context()

	err := db.WithOrgTx(ctx, p.app, rlsOrgA, func(ctx context.Context, q db.Querier) error {
		for table, n := range orgTableCounts(ctx, t, q, rlsOrgA) {
			want := 1
			if table == "memberships" {
				want = 2
			}
			if n != want {
				t.Errorf("scope A, own %s: want %d rows, got %d", table, want, n)
			}
		}
		for table, n := range orgTableCounts(ctx, t, q, rlsOrgB) {
			if n != 0 {
				t.Errorf("scope A, tenant B's %s: want 0 rows, got %d", table, n)
			}
		}
		tag, err := q.Exec(ctx, `UPDATE organization.organizations SET name = 'hijacked' WHERE id = $1`, rlsOrgB)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 0 {
			t.Errorf("scope A updating B: want 0 rows affected, got %d", tag.RowsAffected())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scope A: %v", err)
	}

	for name, sql := range map[string]string{
		"membership": `INSERT INTO organization.memberships (organization_id, auth_sub, role) VALUES ($1, 'rls_mallory', 'member')`,
		"invitation": `INSERT INTO organization.invitations (organization_id, email, role, token, invited_by, expires_at) VALUES ($1, 'm@example.com', 'member', 'rls-token-m', 'rls_mallory', now())`,
	} {
		err := db.WithOrgTx(ctx, p.app, rlsOrgA, func(ctx context.Context, q db.Querier) error {
			_, err := q.Exec(ctx, sql, rlsOrgB)
			return err
		})
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "42501" {
			t.Errorf("scope A inserting a %s into B: want RLS violation 42501, got %v", name, err)
		}
	}
}

// TestRLS_OrganizationTables_NoScopeSeesNothing covers the fail-closed
// property on a fresh connection and on one that previously served a scoped
// transaction (where the setting reads back as ” rather than NULL).
func TestRLS_OrganizationTables_NoScopeSeesNothing(t *testing.T) {
	p := seedRLSFixtures(t)
	ctx := t.Context()
	single := openRLSPool(t, "POSTGRES_APP_URL", 1) // one connection, so the second check reuses it

	for table, n := range orgTableCounts(ctx, t, single, rlsOrgA) {
		if n != 0 {
			t.Errorf("fresh connection, no scope, %s: want 0 rows, got %d", table, n)
		}
	}
	if err := db.WithOrgTx(ctx, single, rlsOrgA, func(context.Context, db.Querier) error { return nil }); err != nil {
		t.Fatalf("scoped transaction: %v", err)
	}
	for table, n := range orgTableCounts(ctx, t, single, rlsOrgA) {
		if n != 0 {
			t.Errorf("reused connection, no scope, %s: want 0 rows, got %d", table, n)
		}
	}
	_ = p
}

// newRLSModule builds the module as cmd/api does: the RLS-constrained main
// pool plus the unscoped pool for the paths with no tenant to scope to.
func newRLSModule(p rlsPools, unscoped bool) *Module {
	mod := NewModuleForTest(p.app)
	if unscoped {
		mod.SetUnscopedPool(p.unscoped)
	}
	return mod
}

func TestRLS_AuthorizationLookups_NeedTheUnscopedPool(t *testing.T) {
	p := seedRLSFixtures(t)
	ctx := t.Context()

	mod := newRLSModule(p, true)
	org, err := mod.GetOrganizationByID(ctx, rlsOrgA)
	if err != nil || org.ID != rlsOrgA {
		t.Fatalf("GetOrganizationByID via unscoped pool: want A, got %v, %v", org, err)
	}
	role, err := mod.GetMemberRole(ctx, rlsOrgA, rlsAlice)
	if err != nil || role != contracts.RoleOwner {
		t.Fatalf("GetMemberRole via unscoped pool: want owner, got %q, %v", role, err)
	}

	// Without it, the lookup runs on the RLS-constrained pool with no scope
	// and finds nothing — why cmd/api asserts the pool at boot.
	if _, err := newRLSModule(p, false).GetOrganizationByID(ctx, rlsOrgA); err == nil {
		t.Error("GetOrganizationByID without the unscoped pool: want not-found under RLS, got nil")
	}
}

func TestRLS_ScopedOperations_WorkUnderStratumApp(t *testing.T) {
	p := seedRLSFixtures(t)
	ctx := t.Context()
	mod := newRLSModule(p, true)

	members, err := mod.svc.listMembers(ctx, rlsOrgA)
	if err != nil || len(members) != 2 {
		t.Fatalf("listMembers(A): want 2, got %d, %v", len(members), err)
	}

	orgs, err := mod.svc.listOrganizations(ctx, rlsAlice)
	if err != nil || len(orgs) != 1 || orgs[0].ID != rlsOrgA {
		t.Fatalf("listOrganizations(alice): want only A, got %v, %v", orgs, err)
	}

	n, err := mod.CountActiveOwnedOrganizations(ctx, rlsAlice)
	if err != nil || n != 1 {
		t.Fatalf("CountActiveOwnedOrganizations(alice): want 1, got %d, %v", n, err)
	}

	created, err := mod.svc.createOrganization(ctx, rlsSlugC, "RLS C", rlsAlice, "US", "solo", "monthly", nil, "")
	if err != nil {
		t.Fatalf("createOrganization under RLS: %v", err)
	}
	var owner string
	if err := p.admin.QueryRow(ctx, `SELECT auth_sub FROM organization.memberships WHERE organization_id = $1 AND role = 'owner'`, created.ID).Scan(&owner); err != nil || owner != rlsAlice {
		t.Fatalf("created organization's owner membership: want %s, got %q, %v", rlsAlice, owner, err)
	}
	if _, err := mod.svc.createOrganization(ctx, "rlstest-b", "dup", rlsAlice, "US", "solo", "monthly", nil, ""); err == nil {
		t.Error("createOrganization with a slug taken by another tenant: want SLUG_TAKEN, got nil")
	}

	// Scope binding: a request authorized for A cannot open B's scope.
	scoped := db.WithAuthorizedOrg(ctx, rlsOrgA)
	if _, err := mod.svc.getOrganization(scoped, rlsOrgB); !errors.Is(err, db.ErrOrgScopeMismatch) {
		t.Errorf("getOrganization(B) under A's authorized scope: want ErrOrgScopeMismatch, got %v", err)
	}
}

type recordingBillingWriter struct {
	contracts.BillingWriter
	mu     sync.Mutex
	counts map[string]int64
	done   chan struct{}
}

func (w *recordingBillingWriter) RecordUsage(_ context.Context, organizationID, _ string, value int64) error {
	w.mu.Lock()
	w.counts[organizationID] = value
	w.mu.Unlock()
	w.done <- struct{}{}
	return nil
}

// TestRLS_CrossModulePorts_StillApply covers the calls other modules make
// into organization with no organization scope of their own — each fails
// silently (0 rows) under RLS unless the port scopes its own transaction.
func TestRLS_CrossModulePorts_StillApply(t *testing.T) {
	p := seedRLSFixtures(t)
	ctx := t.Context()
	mod := newRLSModule(p, true)

	if _, err := p.admin.Exec(ctx, `UPDATE organization.organizations SET status = 'suspended', suspended_reason = 'subscription expired' WHERE id = $1`, rlsOrgA); err != nil {
		t.Fatalf("suspend A: %v", err)
	}
	if err := mod.UnsuspendOrganization(ctx, rlsOrgA, []string{"subscription expired"}); err != nil {
		t.Fatalf("UnsuspendOrganization: %v", err)
	}
	var status string
	if err := p.admin.QueryRow(ctx, `SELECT status FROM organization.organizations WHERE id = $1`, rlsOrgA).Scan(&status); err != nil || status != "active" {
		t.Errorf("after UnsuspendOrganization: want active, got %q, %v", status, err)
	}

	res, err := mod.ResolveDowngradeOverage(ctx, rlsOrgA, nil, 1, true)
	if err != nil || len(res.RemovedMemberAuthSubs) != 1 {
		t.Errorf("ResolveDowngradeOverage(A, limit 1, dry run): want 1 member to remove, got %v, %v", res.RemovedMemberAuthSubs, err)
	}

	writer := &recordingBillingWriter{counts: map[string]int64{}, done: make(chan struct{}, 1)}
	mod.svc.billingWriter = writer
	mod.svc.syncMemberUsage(ctx, rlsOrgA)
	select {
	case <-writer.done:
	case <-time.After(5 * time.Second):
		t.Fatal("syncMemberUsage did not record usage")
	}
	if got := writer.counts[rlsOrgA]; got != 2 {
		t.Errorf("syncMemberUsage(A): want 2 active members recorded, got %d", got)
	}
}
