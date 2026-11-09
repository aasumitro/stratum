package billing_test

import (
	"testing"

	"github.com/aasumitro/stratum/internal/modules/billing"
)

// An upgrade prorates period_end earlier, so usage recorded afterwards lands in
// a row with an earlier end than the one recorded before. The limit check must
// read the latest count, not the row with the latest period_end.
func TestUsage_AfterAnUpgradeShortensThePeriod_ReadsTheLatestCount(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user  = "integ_usage_after_upgrade"
		orgID = "00000000-0000-0000-0000-00000000a5e1"
	)
	setupBillingTest(t, pool, orgID)
	subID := provisionActive(t, pool, orgID, user, "20 days")
	mod := billing.NewModuleForTest(pool, stubRefReader{})

	if err := mod.RecordUsage(t.Context(), orgID, "members", 3); err != nil {
		t.Fatalf("record before upgrade: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`UPDATE billing.subscriptions SET period_end = period_end - interval '10 days' WHERE id = $1`, subID); err != nil {
		t.Fatalf("shorten period: %v", err)
	}
	if err := mod.RecordUsage(t.Context(), orgID, "members", 5); err != nil {
		t.Fatalf("record after upgrade: %v", err)
	}

	current, _, err := mod.CheckUsageLimit(t.Context(), orgID, "members")
	if err != nil {
		t.Fatalf("check usage: %v", err)
	}
	if current != 5 {
		t.Errorf("current members: want 5 (recorded after the upgrade), got %d", current)
	}
}
