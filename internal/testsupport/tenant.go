// Package testsupport provides a reusable harness for asserting cross-tenant
// isolation across org-scoped HTTP routes: seed two independently-owned
// organizations, then drive a table of requests as one org's owner against
// the other org's ID or resources, asserting every one is denied. Shared
// across module test packages (organization, billing) rather than
// duplicated per module, since the seeding/assertion mechanics don't depend
// on which module's routes are under test.
package testsupport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// TwoOrgs holds two independently-owned organizations, seeded directly via
// SQL (bypassing the HTTP creation flow — faster, and this harness tests
// cross-tenant isolation, not creation itself).
type TwoOrgs struct {
	OrgA, OrgB     string // organization IDs
	OwnerA, OwnerB string // auth_sub of each org's owner
}

// SeedTwoOrgs creates two organizations with distinct owners, each seeded
// directly via SQL following organization/service_member_limit_race_test.go's
// insertRaceTestOrg pattern. Does not provision a billing subscription —
// billing-scoped routes need one (see billing/repository_test.go's
// setupBillingTest + mod.Worker.HandleOrganizationCreated), and only
// billing's own test files import the billing module, so that provisioning
// stays there rather than making this shared package depend on billing.
func SeedTwoOrgs(t *testing.T, pool *pgxpool.Pool) TwoOrgs {
	t.Helper()
	ownerA := "ct_owner_a_" + uuid.NewString()
	ownerB := "ct_owner_b_" + uuid.NewString()
	return TwoOrgs{
		OrgA:   insertOrg(t, pool, ownerA),
		OrgB:   insertOrg(t, pool, ownerB),
		OwnerA: ownerA,
		OwnerB: ownerB,
	}
}

func insertOrg(t *testing.T, pool *pgxpool.Pool, ownerSub string) string {
	t.Helper()
	slug := "ct-org-" + uuid.NewString()
	var orgID string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO organization.organizations (slug, name, owner_id, invite_code, invite_code_enabled)
		VALUES ($1, 'Cross-Tenant Test Org', $2, $3, true) RETURNING id`,
		slug, ownerSub, slug+"-code").Scan(&orgID)
	if err != nil {
		t.Fatalf("testsupport: insert org: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO organization.memberships (organization_id, auth_sub, role, joined_at)
		VALUES ($1, $2, 'owner', now())`, orgID, ownerSub); err != nil {
		t.Fatalf("testsupport: insert owner membership: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
	})
	return orgID
}

// CrossTenantCase is one route to probe: OrgA's owner attempts it against
// an OrgB-owned resource (or OrgB's ID directly), asserting it's denied.
type CrossTenantCase struct {
	Name             string
	Method, Path     string
	Body             string
	WantStatus       int
	AssertNoMutation func(t *testing.T, pool *pgxpool.Pool)
}

// RunCrossTenantCases drives engine as authSub against every case, asserting
// WantStatus and, if provided, AssertNoMutation.
//
// pool exists because CrossTenantCase.AssertNoMutation takes a *pgxpool.Pool
// argument to run its direct-SQL check, and this is the only place that has
// one to pass it.
func RunCrossTenantCases(t *testing.T, engine http.Handler, pool *pgxpool.Pool, authSub string, cases []CrossTenantCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httpserver.JSONTestRequest(tc.Method, tc.Path, tc.Body))
			if w.Code != tc.WantStatus {
				t.Errorf("%s as %s %s %s: want %d, got %d: %s", tc.Name, authSub, tc.Method, tc.Path, tc.WantStatus, w.Code, w.Body)
			}
			if tc.AssertNoMutation != nil {
				tc.AssertNoMutation(t, pool)
			}
		})
	}
}
