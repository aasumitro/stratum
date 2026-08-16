package testsupport_test

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/testsupport"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestSeedTwoOrgs_DistinctOwnersAndOrgs is the smoke test for
// internal/testsupport's own correctness: seeding two organizations must
// produce two distinct, real organization rows with two distinct owners —
// the foundation every cross-tenant case in organization/billing's own test
// suites builds on.
func TestSeedTwoOrgs_DistinctOwnersAndOrgs(t *testing.T) {
	pool := testPool(t)
	two := testsupport.SeedTwoOrgs(t, pool)

	if two.OrgA == "" || two.OrgB == "" {
		t.Fatalf("want both org IDs set, got OrgA=%q OrgB=%q", two.OrgA, two.OrgB)
	}
	if two.OrgA == two.OrgB {
		t.Fatalf("want distinct organizations, both seeded as %q", two.OrgA)
	}
	if two.OwnerA == "" || two.OwnerB == "" || two.OwnerA == two.OwnerB {
		t.Fatalf("want distinct, non-empty owners, got OwnerA=%q OwnerB=%q", two.OwnerA, two.OwnerB)
	}

	for _, org := range []struct{ id, owner string }{{two.OrgA, two.OwnerA}, {two.OrgB, two.OwnerB}} {
		var ownerID string
		if err := pool.QueryRow(t.Context(),
			`SELECT owner_id FROM organization.organizations WHERE id = $1`, org.id,
		).Scan(&ownerID); err != nil {
			t.Fatalf("organization %s does not exist: %v", org.id, err)
		}
		if ownerID != org.owner {
			t.Errorf("organization %s: want owner_id=%q, got %q", org.id, org.owner, ownerID)
		}
	}
}
