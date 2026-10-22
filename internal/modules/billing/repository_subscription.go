package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
)

type subscriptionRecord struct {
	ID             string     `json:"id"`
	SubjectType    string     `json:"subject_type"`
	SubjectID      string     `json:"subject_id"`
	Plan           string     `json:"plan"`
	Status         string     `json:"status"`
	Cycle          string     `json:"cycle"`
	Currency       string     `json:"currency"`
	PeriodStart    *time.Time `json:"period_start,omitempty"`
	PeriodEnd      *time.Time `json:"period_end,omitempty"`
	TrialEnd       *time.Time `json:"trial_end,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ScheduledPlan  *string    `json:"scheduled_plan,omitempty"`
	ScheduledCycle *string    `json:"scheduled_cycle,omitempty"`
	// ScheduledRequestedAt is write/scan-only: the API only surfaces a
	// "when was this requested" timestamp on the per-addon row, never on
	// the subscription itself.
	ScheduledRequestedAt *time.Time `json:"-"`
	ScheduledCancelAt    *time.Time `json:"scheduled_cancel_at,omitempty"`
}

const subsCols = `id, subject_type, subject_id, plan, status, cycle, currency, period_start, period_end, trial_end, created_at, updated_at,
	scheduled_plan, scheduled_cycle, scheduled_requested_at, scheduled_cancel_at`

func (r *repository) findSubscriptionBySubject(
	ctx context.Context, q db.Querier,
	subjectType, subjectID string,
) (*subscriptionRecord, error) {
	s := new(subscriptionRecord)
	err := q.QueryRow(ctx,
		`SELECT `+subsCols+` FROM billing.subscriptions WHERE subject_type = $1 AND subject_id = $2`,
		subjectType, subjectID,
	).Scan(&s.ID, &s.SubjectType, &s.SubjectID, &s.Plan, &s.Status,
		&s.Cycle, &s.Currency, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CreatedAt, &s.UpdatedAt,
		&s.ScheduledPlan, &s.ScheduledCycle, &s.ScheduledRequestedAt, &s.ScheduledCancelAt)
	if err != nil {
		return nil, fmt.Errorf("billing.findSubscriptionBySubject: %w", err)
	}
	return s, nil
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
	db.RequireTx(q)
	var id string
	err := q.QueryRow(ctx, `SELECT id FROM billing.subscriptions WHERE id = $1 FOR UPDATE`, subscriptionID).Scan(&id)
	if err != nil {
		return fmt.Errorf("billing.lockSubscriptionForUpdate: %w", err)
	}
	return nil
}

func (r *repository) insertSubscription(
	ctx context.Context, q db.Querier,
	subjectType, subjectID, plan, cycle, currency string,
) (*subscriptionRecord, error) {
	s := new(subscriptionRecord)
	now := time.Now()
	periodEnd := now.AddDate(0, 1, 0)
	if cycle == cycleYearly {
		periodEnd = now.AddDate(1, 0, 0)
	}
	err := q.QueryRow(ctx,
		`INSERT INTO billing.subscriptions (subject_type, subject_id, plan, status, cycle, currency, period_start, period_end)
		VALUES ($1, $2, $3, 'active', $4, $5, $6, $7)
		ON CONFLICT (subject_type, subject_id) DO NOTHING
		RETURNING `+subsCols,
		subjectType, subjectID, plan, cycle, currency, now, periodEnd,
	).Scan(&s.ID, &s.SubjectType, &s.SubjectID, &s.Plan, &s.Status,
		&s.Cycle, &s.Currency, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CreatedAt, &s.UpdatedAt,
		&s.ScheduledPlan, &s.ScheduledCycle, &s.ScheduledRequestedAt, &s.ScheduledCancelAt)
	if err != nil {
		return nil, fmt.Errorf("billing.insertSubscription: %w", err)
	}
	return s, nil
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
		&s.Cycle, &s.Currency, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CreatedAt, &s.UpdatedAt,
		&s.ScheduledPlan, &s.ScheduledCycle, &s.ScheduledRequestedAt, &s.ScheduledCancelAt)
	if err != nil {
		return nil, fmt.Errorf("billing.insertSubscriptionWithTrial: %w", err)
	}
	return s, nil
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
		&s.Cycle, &s.Currency, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CreatedAt, &s.UpdatedAt,
		&s.ScheduledPlan, &s.ScheduledCycle, &s.ScheduledRequestedAt, &s.ScheduledCancelAt)
	if err != nil {
		return nil, fmt.Errorf("billing.updateSubscriptionPlan: %w", err)
	}
	return s, nil
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
		&s.Cycle, &s.Currency, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CreatedAt, &s.UpdatedAt,
		&s.ScheduledPlan, &s.ScheduledCycle, &s.ScheduledRequestedAt, &s.ScheduledCancelAt)
	if err != nil {
		return nil, fmt.Errorf("billing.updateSubscriptionStatus: %w", err)
	}
	return s, nil
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
		&s.Cycle, &s.Currency, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CreatedAt, &s.UpdatedAt,
		&s.ScheduledPlan, &s.ScheduledCycle, &s.ScheduledRequestedAt, &s.ScheduledCancelAt)
	if err != nil {
		return nil, fmt.Errorf("billing.activateTrialImmediately: %w", err)
	}
	return s, nil
}

// overdueSubscription is the minimal projection ReconcileOverdueSubscriptions
// needs to re-enqueue a missed expiry check.
type overdueSubscription struct {
	ID          string
	SubjectType string
	SubjectID   string
	IsTrial     bool
	ExpectedEnd time.Time
}

// listOverdueSubscriptions returns every active/trialing subscription whose
// current period end (trial_end for a trial, else period_end) is already in
// the past — the set whose scheduled SubscriptionCheck should have expired
// them but may have been lost. past_due is deliberately excluded: it reaches
// the same expireIfDue path once its period_end passes and needs no
// reconciliation here.
func (r *repository) listOverdueSubscriptions(ctx context.Context, q db.Querier) ([]overdueSubscription, error) {
	rows, err := q.Query(ctx, `
		SELECT id, subject_type, subject_id,
		       status = 'trialing' AS is_trial,
		       COALESCE(trial_end, period_end) AS expected_end
		FROM billing.subscriptions
		WHERE status IN ('active', 'trialing')
		  AND COALESCE(trial_end, period_end) < now()`)
	if err != nil {
		return nil, fmt.Errorf("billing.listOverdueSubscriptions: %w", err)
	}

	var out []overdueSubscription
	for rows.Next() {
		var s overdueSubscription
		if err := rows.Scan(&s.ID, &s.SubjectType, &s.SubjectID, &s.IsTrial, &s.ExpectedEnd); err != nil {
			rows.Close()
			return nil, fmt.Errorf("billing.listOverdueSubscriptions: scan: %w", err)
		}
		out = append(out, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing.listOverdueSubscriptions: %w", err)
	}
	return out, nil
}

func (r *repository) findSubscriptionByID(ctx context.Context, q db.Querier, id string) (*subscriptionRecord, error) {
	s := new(subscriptionRecord)
	err := q.QueryRow(ctx,
		`SELECT `+subsCols+` FROM billing.subscriptions WHERE id = $1`, id,
	).Scan(&s.ID, &s.SubjectType, &s.SubjectID, &s.Plan, &s.Status,
		&s.Cycle, &s.Currency, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CreatedAt, &s.UpdatedAt,
		&s.ScheduledPlan, &s.ScheduledCycle, &s.ScheduledRequestedAt, &s.ScheduledCancelAt)
	if err != nil {
		return nil, fmt.Errorf("billing.findSubscriptionByID: %w", err)
	}
	return s, nil
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
		&s.Cycle, &s.Currency, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CreatedAt, &s.UpdatedAt,
		&s.ScheduledPlan, &s.ScheduledCycle, &s.ScheduledRequestedAt, &s.ScheduledCancelAt)
	if err != nil {
		return nil, fmt.Errorf("billing.updateSubscriptionPlanAndPeriod: %w", err)
	}
	return s, nil
}

// countSubscriptionsBySubjectIDs counts subscriptions belonging to any of
// the given organization subject IDs. The
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
	if err != nil {
		return 0, fmt.Errorf("billing.countSubscriptionsBySubjectIDs: %w", err)
	}
	return count, nil
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
	if err != nil {
		return fmt.Errorf("billing.updateSubscriptionPeriod: %w", err)
	}
	return nil
}

// clearTrialEndAndUpdatePeriod sets the period and clears trial_end in one
// statement — used when a subscription is definitively leaving the trialing
// state (expired-reactivation, or resume-from-cancelled past the trial
// window). The shared updateSubscriptionPeriod is intentionally not modified
// to avoid breaking the isStillTrialing branch that must preserve trial_end.
func (r *repository) clearTrialEndAndUpdatePeriod(
	ctx context.Context, q db.Querier,
	id string, periodStart, periodEnd time.Time,
) error {
	_, err := q.Exec(ctx, `
		UPDATE billing.subscriptions
		SET period_start = $2, period_end = $3, trial_end = NULL, updated_at = now()
		WHERE id = $1`,
		id, periodStart, periodEnd,
	)
	if err != nil {
		return fmt.Errorf("billing.clearTrialEndAndUpdatePeriod: %w", err)
	}
	return nil
}

// updateSubscriptionCycleAndPeriod mirrors updateSubscriptionPeriod but also
// sets cycle, atomically in the same statement — used by the extend
// switch-to-annual path so the cycle conversion and the period extension it
// pays for always land together.
func (r *repository) updateSubscriptionCycleAndPeriod(
	ctx context.Context, q db.Querier,
	id, cycle string, periodStart, periodEnd time.Time,
) error {
	_, err := q.Exec(ctx, `
		UPDATE billing.subscriptions
		SET cycle = $2, period_start = $3, period_end = $4, updated_at = now()
		WHERE id = $1`,
		id, cycle, periodStart, periodEnd,
	)
	if err != nil {
		return fmt.Errorf("billing.updateSubscriptionCycleAndPeriod: %w", err)
	}
	return nil
}

// schedulePlanDowngrade records a future plan/cycle change on an active
// subscription without touching its live plan/cycle — the renewal worker
// applies it later via applyScheduledPlanDowngrade. Re-scheduling overwrites
// whatever was previously scheduled (latest-wins, no history of prior
// schedule attempts kept here).
func (r *repository) schedulePlanDowngrade(ctx context.Context, q db.Querier, subscriptionID, plan, cycle string) error {
	_, err := q.Exec(ctx, `
		UPDATE billing.subscriptions
		SET scheduled_plan = $2, scheduled_cycle = $3, scheduled_requested_at = now(), updated_at = now()
		WHERE id = $1`,
		subscriptionID, plan, cycle,
	)
	if err != nil {
		return fmt.Errorf("billing.schedulePlanDowngrade: %w", err)
	}
	return nil
}

// clearScheduledPlanDowngrade undoes a scheduled plan downgrade — the live
// plan/cycle are never touched, since the scheduled values never applied.
func (r *repository) clearScheduledPlanDowngrade(ctx context.Context, q db.Querier, subscriptionID string) error {
	_, err := q.Exec(ctx, `
		UPDATE billing.subscriptions
		SET scheduled_plan = NULL, scheduled_cycle = NULL, scheduled_requested_at = NULL, updated_at = now()
		WHERE id = $1`,
		subscriptionID,
	)
	if err != nil {
		return fmt.Errorf("billing.clearScheduledPlanDowngrade: %w", err)
	}
	return nil
}

// applyScheduledPlanDowngrade moves a scheduled plan/cycle into the live
// columns and clears the schedule, in one statement. The WHERE guard makes
// this a safe no-op (pgx.ErrNoRows) when nothing is scheduled — the caller
// must treat that as "nothing to do", not an error.
func (r *repository) applyScheduledPlanDowngrade(ctx context.Context, q db.Querier, subscriptionID string) (*subscriptionRecord, error) {
	s := new(subscriptionRecord)
	err := q.QueryRow(ctx, `
		UPDATE billing.subscriptions
		SET plan = scheduled_plan, cycle = scheduled_cycle,
		    scheduled_plan = NULL, scheduled_cycle = NULL, scheduled_requested_at = NULL,
		    updated_at = now()
		WHERE id = $1 AND scheduled_plan IS NOT NULL
		RETURNING `+subsCols,
		subscriptionID,
	).Scan(&s.ID, &s.SubjectType, &s.SubjectID, &s.Plan, &s.Status,
		&s.Cycle, &s.Currency, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CreatedAt, &s.UpdatedAt,
		&s.ScheduledPlan, &s.ScheduledCycle, &s.ScheduledRequestedAt, &s.ScheduledCancelAt)
	if err != nil {
		return nil, fmt.Errorf("billing.applyScheduledPlanDowngrade: %w", err)
	}
	return s, nil
}

// scheduleCancellation marks an active subscription to terminate at
// renewal. It deliberately does not clear scheduled_plan/scheduled_cycle —
// the caller clears those (and every scheduled addon change) explicitly in
// the same transaction, so the "cancellation supersedes everything else"
// behavior is visible at the call site rather than hidden in here.
func (r *repository) scheduleCancellation(ctx context.Context, q db.Querier, subscriptionID string) error {
	_, err := q.Exec(ctx, `
		UPDATE billing.subscriptions SET scheduled_cancel_at = now(), updated_at = now() WHERE id = $1`,
		subscriptionID,
	)
	if err != nil {
		return fmt.Errorf("billing.scheduleCancellation: %w", err)
	}
	return nil
}

// clearScheduledCancellation undoes a scheduled cancellation. status is
// untouched — it was never changed by scheduling the cancellation in the
// first place.
func (r *repository) clearScheduledCancellation(ctx context.Context, q db.Querier, subscriptionID string) error {
	_, err := q.Exec(ctx, `
		UPDATE billing.subscriptions SET scheduled_cancel_at = NULL, updated_at = now() WHERE id = $1`,
		subscriptionID,
	)
	if err != nil {
		return fmt.Errorf("billing.clearScheduledCancellation: %w", err)
	}
	return nil
}
