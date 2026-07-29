package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
)

type couponRecord struct {
	Code           string     `json:"code"`
	Name           string     `json:"name"`
	DiscountType   string     `json:"discount_type"`
	AmountCents    *int64     `json:"amount_cents,omitempty"`
	PercentOff     *int16     `json:"percent_off,omitempty"`
	Currency       *string    `json:"currency,omitempty"`
	Cadence        string     `json:"cadence"`
	DurationCount  *int       `json:"duration_count,omitempty"`
	ValidFrom      *time.Time `json:"valid_from,omitempty"`
	ValidUntil     *time.Time `json:"valid_until,omitempty"`
	MaxRedemptions *int       `json:"max_redemptions,omitempty"`
	RedeemedCount  int        `json:"redeemed_count"`
	Active         bool       `json:"active"`
}

// couponRedemptionRecord is a subscription's redemption of an active
// coupon, joined with enough of the coupon's shape to both decide cadence
// applicability (couponStillApplicable) and compute a discount amount,
// without a second round trip.
type couponRedemptionRecord struct {
	CouponCode    string
	DiscountType  string
	AmountCents   *int64
	PercentOff    *int16
	Cadence       string
	AppliedCount  int
	DurationCount *int
}

func (r *repository) findCouponByCode(ctx context.Context, q db.Querier, code string) (*couponRecord, error) {
	var c couponRecord
	err := q.QueryRow(ctx, `
		SELECT code, name, discount_type, amount_cents, percent_off, currency, cadence,
		       duration_count, valid_from, valid_until, max_redemptions, redeemed_count, active
		FROM billing.coupons
		WHERE code = $1`, code,
	).Scan(
		&c.Code, &c.Name, &c.DiscountType, &c.AmountCents, &c.PercentOff, &c.Currency, &c.Cadence,
		&c.DurationCount, &c.ValidFrom, &c.ValidUntil, &c.MaxRedemptions, &c.RedeemedCount, &c.Active,
	)
	if err != nil {
		return nil, fmt.Errorf("billing.findCouponByCode: %w", err)
	}
	return &c, nil
}

// isCouponRedeemableBySubject reports whether code's target allow-list
// (billing.coupon_targets) permits this organization/user — an empty
// allow-list means anyone may redeem.
func (r *repository) isCouponRedeemableBySubject(
	ctx context.Context, q db.Querier, code, organizationID, authSub string,
) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `
		SELECT NOT EXISTS(SELECT 1 FROM billing.coupon_targets WHERE coupon_id = $1)
		    OR EXISTS(
		        SELECT 1 FROM billing.coupon_targets
		        WHERE coupon_id = $1
		          AND ((subject_type = 'organization' AND subject_id = $2)
		               OR (subject_type = 'user' AND subject_id = $3))
		    )`, code, organizationID, authSub,
	).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("billing.isCouponRedeemableBySubject: %w", err)
	}
	return ok, nil
}

// listEligibleCoupons returns every coupon this organization/user could
// currently redeem — active, within its validity window, not exhausted
// (max_redemptions), and either untargeted or explicitly targeted at this
// org/user (same allow-list rule as isCouponRedeemableBySubject). Backs the
// Pay dialog's coupon list — coupons are listed, never auto-applied.
func (r *repository) listEligibleCoupons(
	ctx context.Context, q db.Querier, organizationID, authSub string,
) ([]couponRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT code, name, discount_type, amount_cents, percent_off, currency, cadence,
		       duration_count, valid_from, valid_until, max_redemptions, redeemed_count, active
		FROM billing.coupons c
		WHERE active = true
		  AND (valid_from IS NULL OR valid_from <= now())
		  AND (valid_until IS NULL OR valid_until >= now())
		  AND (max_redemptions IS NULL OR redeemed_count < max_redemptions)
		  AND (
		    NOT EXISTS(SELECT 1 FROM billing.coupon_targets WHERE coupon_id = c.code)
		    OR EXISTS(
		        SELECT 1 FROM billing.coupon_targets
		        WHERE coupon_id = c.code
		          AND ((subject_type = 'organization' AND subject_id = $1)
		               OR (subject_type = 'user' AND subject_id = $2))
		    )
		  )
		ORDER BY c.code`, organizationID, authSub,
	)
	if err != nil {
		return nil, fmt.Errorf("billing.listEligibleCoupons: %w", err)
	}
	defer rows.Close()

	var out []couponRecord
	for rows.Next() {
		var c couponRecord
		if err := rows.Scan(
			&c.Code, &c.Name, &c.DiscountType, &c.AmountCents, &c.PercentOff, &c.Currency, &c.Cadence,
			&c.DurationCount, &c.ValidFrom, &c.ValidUntil, &c.MaxRedemptions, &c.RedeemedCount, &c.Active,
		); err != nil {
			return nil, fmt.Errorf("billing.listEligibleCoupons: scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing.listEligibleCoupons: %w", err)
	}
	return out, nil
}

func (r *repository) insertCouponRedemption(ctx context.Context, q db.Querier, code, subscriptionID string) error {
	_, err := q.Exec(ctx,
		`INSERT INTO billing.coupon_redemptions (coupon_id, subscription_id) VALUES ($1, $2)`,
		code, subscriptionID)
	if err != nil {
		return fmt.Errorf("billing.insertCouponRedemption: %w", err)
	}
	return nil
}

// tryIncrementCouponRedeemedCount atomically increments the redemption count
// if the coupon hasn't reached its max_redemptions. It returns true if
// incremented, false if exhausted.
func (r *repository) tryIncrementCouponRedeemedCount(ctx context.Context, q db.Querier, code string) (bool, error) {
	ct, err := q.Exec(ctx, `
		UPDATE billing.coupons
		SET redeemed_count = redeemed_count + 1
		WHERE code = $1
		  AND (max_redemptions IS NULL OR redeemed_count < max_redemptions)`, code)
	if err != nil {
		return false, fmt.Errorf("billing.tryIncrementCouponRedeemedCount: %w", err)
	}
	return ct.RowsAffected() == 1, nil
}

// listCouponRedemptionsForSubscription returns every redemption for
// subscriptionID whose coupon is still active, newest first. Cadence
// exhaustion (once/repeated/forever) is deliberately NOT filtered in SQL —
// see couponStillApplicable, kept as a pure Go function so it's unit
// testable without a database.
func (r *repository) listCouponRedemptionsForSubscription(
	ctx context.Context, q db.Querier, subscriptionID string,
) ([]couponRedemptionRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT c.code, c.discount_type, c.amount_cents, c.percent_off, c.cadence, cr.applied_count, c.duration_count
		FROM billing.coupon_redemptions cr
		JOIN billing.coupons c ON c.code = cr.coupon_id
		WHERE cr.subscription_id = $1 AND c.active = true
		ORDER BY cr.redeemed_at DESC`, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("billing.listCouponRedemptionsForSubscription: %w", err)
	}
	defer rows.Close()

	var out []couponRedemptionRecord
	for rows.Next() {
		var cr couponRedemptionRecord
		if err := rows.Scan(&cr.CouponCode, &cr.DiscountType, &cr.AmountCents, &cr.PercentOff, &cr.Cadence, &cr.AppliedCount, &cr.DurationCount); err != nil {
			return nil, fmt.Errorf("billing.listCouponRedemptionsForSubscription: scan: %w", err)
		}
		out = append(out, cr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing.listCouponRedemptionsForSubscription: %w", err)
	}
	return out, nil
}

func (r *repository) incrementCouponRedemptionApplied(ctx context.Context, q db.Querier, subscriptionID, couponCode string) error {
	_, err := q.Exec(ctx,
		`UPDATE billing.coupon_redemptions SET applied_count = applied_count + 1 WHERE subscription_id = $1 AND coupon_id = $2`,
		subscriptionID, couponCode)
	if err != nil {
		return fmt.Errorf("billing.incrementCouponRedemptionApplied: %w", err)
	}
	return nil
}
