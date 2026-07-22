package billing

import (
	"context"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
)

type subscriptionRecord struct {
	ID          string     `json:"id"`
	SubjectType string     `json:"subject_type"`
	SubjectID   string     `json:"subject_id"`
	Plan        string     `json:"plan"`
	Status      string     `json:"status"`
	Cycle       string     `json:"cycle"`
	Currency    string     `json:"currency"`
	PeriodStart *time.Time `json:"period_start,omitempty"`
	PeriodEnd   *time.Time `json:"period_end,omitempty"`
	TrialEnd    *time.Time `json:"trial_end,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

const subsCols = `id, subject_type, subject_id, plan, status, cycle, currency, period_start, period_end, trial_end, created_at, updated_at`

func (r *repository) findSubscriptionBySubject(
	ctx context.Context, q db.Querier,
	subjectType, subjectID string,
) (*subscriptionRecord, error) {
	s := new(subscriptionRecord)
	err := q.QueryRow(ctx,
		`SELECT `+subsCols+` FROM billing.subscriptions WHERE subject_type = $1 AND subject_id = $2`,
		subjectType, subjectID,
	).Scan(&s.ID, &s.SubjectType, &s.SubjectID, &s.Plan, &s.Status,
		&s.Cycle, &s.Currency, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

// lockSubscriptionForUpdate takes a row lock on the subscription for the
// rest of the caller's transaction. Used by redeemCoupon so two concurrent
// redemptions for the same subscription serialize on the
// check-then-insert (findActiveCouponRedemption + insertCouponRedemption)
// instead of both passing the "no active coupon yet" check and attaching
// two active coupons — there's no way to express "one active redemption"
// as a DB constraint since a redemption's active/exhausted status is
// computed from the coupon's cadence and applied_count, not stored.
func (r *repository) lockSubscriptionForUpdate(ctx context.Context, q db.Querier, subscriptionID string) error {
	var id string
	return q.QueryRow(ctx, `SELECT id FROM billing.subscriptions WHERE id = $1 FOR UPDATE`, subscriptionID).Scan(&id)
}

func (r *repository) insertSubscription(
	ctx context.Context, q db.Querier,
	subjectType, subjectID, plan, cycle, currency string,
) (*subscriptionRecord, error) {
	s := new(subscriptionRecord)
	now := time.Now()
	periodEnd := now.AddDate(0, 1, 0)
	err := q.QueryRow(ctx,
		`INSERT INTO billing.subscriptions (subject_type, subject_id, plan, status, cycle, currency, period_start, period_end)
		VALUES ($1, $2, $3, 'active', $4, $5, $6, $7)
		ON CONFLICT (subject_type, subject_id) DO NOTHING
		RETURNING `+subsCols,
		subjectType, subjectID, plan, cycle, currency, now, periodEnd,
	).Scan(&s.ID, &s.SubjectType, &s.SubjectID, &s.Plan, &s.Status,
		&s.Cycle, &s.Currency, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

func (r *repository) insertSubscriptionWithTrial(
	ctx context.Context, q db.Querier,
	subjectType, subjectID, plan, cycle, currency string, trialEnd time.Time,
) (*subscriptionRecord, error) {
	s := new(subscriptionRecord)
	now := time.Now()
	err := q.QueryRow(ctx,
		`INSERT INTO billing.subscriptions (subject_type, subject_id, plan, status, cycle, currency, period_start, period_end, trial_end)
		VALUES ($1, $2, $3, 'trialing', $4, $5, $6, $7, $7)
		ON CONFLICT (subject_type, subject_id) DO NOTHING
		RETURNING `+subsCols,
		subjectType, subjectID, plan, cycle, currency, now, trialEnd,
	).Scan(&s.ID, &s.SubjectType, &s.SubjectID, &s.Plan, &s.Status,
		&s.Cycle, &s.Currency, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

func (r *repository) updateSubscriptionPlan(
	ctx context.Context, q db.Querier,
	id, plan, cycle string,
) (*subscriptionRecord, error) {
	s := new(subscriptionRecord)
	err := q.QueryRow(ctx,
		`UPDATE billing.subscriptions SET plan = $2, cycle = $3, updated_at = now()
		WHERE id = $1 RETURNING `+subsCols,
		id, plan, cycle,
	).Scan(&s.ID, &s.SubjectType, &s.SubjectID, &s.Plan, &s.Status,
		&s.Cycle, &s.Currency, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

func (r *repository) updateSubscriptionStatus(
	ctx context.Context, q db.Querier,
	id, status string,
) (*subscriptionRecord, error) {
	s := new(subscriptionRecord)
	err := q.QueryRow(ctx,
		`UPDATE billing.subscriptions SET status = $2, updated_at = now()
		WHERE id = $1 RETURNING `+subsCols,
		id, status,
	).Scan(&s.ID, &s.SubjectType, &s.SubjectID, &s.Plan, &s.Status,
		&s.Cycle, &s.Currency, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

// activateTrialImmediately converts a trialing subscription to active right
// now instead of waiting for trial_end: sets a fresh billing period starting
// now and, critically, clears trial_end. Without clearing it, expireIfDue's
// existing trial_end check would still fire at the original trial_end date
// and incorrectly mark this now-active, now-invoiced subscription expired.
func (r *repository) activateTrialImmediately(
	ctx context.Context, q db.Querier,
	id string, periodStart, periodEnd time.Time,
) (*subscriptionRecord, error) {
	s := new(subscriptionRecord)
	err := q.QueryRow(ctx,
		`UPDATE billing.subscriptions
		SET status = 'active', period_start = $2, period_end = $3, trial_end = NULL, updated_at = now()
		WHERE id = $1 RETURNING `+subsCols,
		id, periodStart, periodEnd,
	).Scan(&s.ID, &s.SubjectType, &s.SubjectID, &s.Plan, &s.Status,
		&s.Cycle, &s.Currency, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

func (r *repository) findSubscriptionByID(ctx context.Context, q db.Querier, id string) (*subscriptionRecord, error) {
	s := new(subscriptionRecord)
	err := q.QueryRow(ctx,
		`SELECT `+subsCols+` FROM billing.subscriptions WHERE id = $1`, id,
	).Scan(&s.ID, &s.SubjectType, &s.SubjectID, &s.Plan, &s.Status,
		&s.Cycle, &s.Currency, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

func (r *repository) updateSubscriptionPlanAndPeriod(
	ctx context.Context, q db.Querier,
	id, plan, cycle string, periodEnd time.Time,
) (*subscriptionRecord, error) {
	s := new(subscriptionRecord)
	err := q.QueryRow(ctx,
		`UPDATE billing.subscriptions SET plan = $2, cycle = $3, status = 'active', period_end = $4, updated_at = now()
		WHERE id = $1 RETURNING `+subsCols,
		id, plan, cycle, periodEnd,
	).Scan(&s.ID, &s.SubjectType, &s.SubjectID, &s.Plan, &s.Status,
		&s.Cycle, &s.Currency, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

// countSubscriptionsBySubjectIDs counts subscriptions belonging to any of
// the given organization subject IDs — replaces the cross-schema JOIN +
// implicit UUID->TEXT cast countSubscriptionsByOwner used to need, with a
// plain lookup against the existing idx_subscriptions_subject index. The
// caller resolves owned organization IDs via
// contracts.OrganizationReader.ListOwnedOrganizationIDs first.
func (r *repository) countSubscriptionsBySubjectIDs(ctx context.Context, q db.Querier, subjectIDs []string) (int, error) {
	if len(subjectIDs) == 0 {
		return 0, nil
	}
	var count int
	err := q.QueryRow(ctx, `
		SELECT COUNT(*) FROM billing.subscriptions
		WHERE subject_type = 'organization' AND subject_id = ANY($1)`,
		subjectIDs,
	).Scan(&count)
	return count, err
}

func (r *repository) updateSubscriptionPeriod(
	ctx context.Context, q db.Querier,
	id string, periodStart, periodEnd time.Time,
) error {
	_, err := q.Exec(ctx, `
		UPDATE billing.subscriptions
		SET period_start = $2, period_end = $3, updated_at = now()
		WHERE id = $1`,
		id, periodStart, periodEnd,
	)
	return err
}
