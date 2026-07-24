package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/db"
)

// couponStillApplicable reports whether a coupon redemption with the given
// cadence and applied_count can still discount another invoice: forever is
// unbounded, once allows exactly one invoice, repeated allows up to
// durationCount invoices (nil duration_count means it's already exhausted).
func couponStillApplicable(cadence string, appliedCount int, durationCount *int) bool {
	switch cadence {
	case cadenceForever:
		return true
	case cadenceOnce:
		return appliedCount == 0
	case cadenceRepeated:
		return durationCount != nil && appliedCount < *durationCount
	default:
		return false
	}
}

// findActiveCouponRedemption returns the most recent still-applicable
// redemption for subscriptionID, or pgx.ErrNoRows if none qualifies.
// Cadence exhaustion is resolved in Go (couponStillApplicable) rather than
// SQL so it stays unit-testable without a database.
func (s *service) findActiveCouponRedemption(ctx context.Context, q db.Querier, subscriptionID string) (*couponRedemptionRecord, error) {
	candidates, err := s.repo.listCouponRedemptionsForSubscription(ctx, q, subscriptionID)
	if err != nil {
		return nil, err
	}
	for i := range candidates {
		if couponStillApplicable(candidates[i].Cadence, candidates[i].AppliedCount, candidates[i].DurationCount) {
			return &candidates[i], nil
		}
	}
	return nil, pgx.ErrNoRows
}

// listEligibleCoupons backs the Pay dialog's coupon list — every coupon
// this organization/user could currently redeem, listed so the owner can
// Apply one explicitly. Never auto-applied.
func (s *service) listEligibleCoupons(ctx context.Context, organizationID, authSub string) ([]couponRecord, error) {
	coupons, err := s.repo.listEligibleCoupons(ctx, s.querier(ctx), organizationID, authSub)
	if err != nil {
		return nil, apperr.Internal("COUPONS_FETCH_FAILED", "failed to list eligible coupons", err)
	}
	return coupons, nil
}

// validateCouponForSubject backs contracts.CatalogReader.ValidateCouponCode
// — the organization module's up-front "cart" validation before an org (and
// its subscription) exist. Reuses listEligibleCoupons with an empty
// organizationID: org-targeted coupons correctly never match (there is no
// org yet to target), user-targeted and untargeted coupons do.
func (s *service) validateCouponForSubject(ctx context.Context, code, authSub string) error {
	coupons, err := s.repo.listEligibleCoupons(ctx, s.querier(ctx), "", authSub)
	if err != nil {
		return err
	}
	for _, c := range coupons {
		if c.Code == code {
			return nil
		}
	}
	return ErrCouponNotRedeemable
}

// redeemCoupon validates code against billing.coupons/coupon_targets and,
// if applicable, records a redemption for organizationID's subscription.
// The discount itself isn't applied here — it lands on the next invoice via
// composeInvoiceAmount, per the coupon's cadence (once/repeated/forever).
// Deliberate scope decision: only one active (non-exhausted) redemption per
// subscription at a time — simplest model, matches how most SaaS products
// behave; relax later if stacking coupons is explicitly requested.
func (s *service) redeemCoupon(ctx context.Context, organizationID, authSub, code string) (err error) {
	defer func() {
		if err == nil {
			return
		}
		switch {
		case errors.Is(err, ErrCouponNotFound):
			err = apperr.NotFound("COUPON_NOT_FOUND", "coupon not found", err)
		case errors.Is(err, ErrCouponNotRedeemable):
			err = apperr.Validation("COUPON_NOT_REDEEMABLE", err.Error())
		default:
			err = apperr.Internal("COUPON_REDEEM_FAILED", "failed to redeem coupon", err)
		}
	}()

	q := s.querier(ctx)

	coupon, err := s.repo.findCouponByCode(ctx, q, code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCouponNotFound
		}
		return err
	}
	if !coupon.Active {
		return fmt.Errorf("%w: inactive", ErrCouponNotRedeemable)
	}
	now := time.Now()
	if coupon.ValidFrom != nil && now.Before(*coupon.ValidFrom) {
		return fmt.Errorf("%w: not yet valid", ErrCouponNotRedeemable)
	}
	if coupon.ValidUntil != nil && now.After(*coupon.ValidUntil) {
		return fmt.Errorf("%w: expired", ErrCouponNotRedeemable)
	}
	if coupon.MaxRedemptions != nil && coupon.RedeemedCount >= *coupon.MaxRedemptions {
		return fmt.Errorf("%w: max redemptions reached", ErrCouponNotRedeemable)
	}

	ok, err := s.repo.isCouponRedeemableBySubject(ctx, q, code, organizationID, authSub)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: not targeted to this organization or user", ErrCouponNotRedeemable)
	}

	sub, err := s.repo.findSubscriptionBySubject(ctx, q, subjectTypeOrganization, organizationID)
	if err != nil {
		return err
	}
	// Lock the subscription for the rest of this transaction so two
	// concurrent redemptions can't both pass the "no active coupon yet"
	// check below and attach two active coupons — the second call blocks
	// here until the first commits or rolls back.
	if err := s.repo.lockSubscriptionForUpdate(ctx, q, sub.ID); err != nil {
		return err
	}
	if _, err := s.findActiveCouponRedemption(ctx, q, sub.ID); err == nil {
		return fmt.Errorf("%w: subscription already has an active coupon", ErrCouponNotRedeemable)
	}

	if err := s.repo.insertCouponRedemption(ctx, q, code, sub.ID); err != nil {
		return err
	}
	_ = s.repo.incrementCouponRedeemedCount(ctx, q, code) // best-effort accounting, no lock — see incrementCouponRedeemedCount
	return nil
}
