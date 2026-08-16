package billing_test

import (
	"testing"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/billing"
)

// TestAttachCartSelections_CouponExhausted_ReturnsError verifies that when
// a cart-selected coupon is exhausted by the time organization provisioning
// attaches it, the whole provisioning fails instead of silently dropping
// the discount — a deliberate product choice (fail loud, let the user pick
// a different coupon) over the previous silent-success behavior.
func TestAttachCartSelections_CouponExhausted_ReturnsError(t *testing.T) {
	pool := testPoolBilling(t)
	const (
		user           = "integ_billing_cart_coupon_exhausted_user"
		couponCode     = "EXHAUSTED_C1002"
		maxRedemptions = 0 // already exhausted
	)

	zero := maxRedemptions
	seedCoupon(t, pool, couponCode, couponSeedOpts{
		DiscountType:   "fixed",
		AmountCents:    couponAmount(100),
		Cadence:        "once",
		MaxRedemptions: &zero,
		RedeemedCount:  zero,
	})

	mod := billing.NewModuleForTest(pool, stubRefReader{})

	orgID := "00000000-0000-0000-0000-0000c0001003"
	setupBillingTest(t, pool, orgID)
	t.Cleanup(func() { cleanupBillingByOrganization(pool, orgID) })

	evt := encodeOrganizationCreatedEventWithCart(
		orgID, user, "solo", "monthly",
		[]events.AddonSelection{},
		couponCode,
	)

	err := mod.Worker.HandleOrganizationCreated(t.Context(), evt)
	if err == nil {
		t.Fatal("want HandleOrganizationCreated to fail with exhausted coupon, got nil")
	}

	// The whole transaction must roll back — no half-provisioned subscription left behind.
	var subCount int
	if err := pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM billing.subscriptions WHERE subject_id = $1`, orgID,
	).Scan(&subCount); err != nil {
		t.Fatalf("query subscriptions: %v", err)
	}
	if subCount != 0 {
		t.Errorf("want no subscription after failed coupon attachment (rollback), got %d", subCount)
	}
}
