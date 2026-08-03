package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/db"
)

// provisionSubscription's count == 0 check below decides trial eligibility:
// an owner with no prior subscription across any owned organization gets a
// trial. count stays 0 (fail open: "treat as first-time") when orgReader is
// unwired — a real lookup error is still fatal, same as before this used
// contracts.OrganizationReader instead of a cross-schema JOIN.
func (s *service) provisionSubscription(
	ctx context.Context, subjectType, subjectID, plan, cycle, createdBy, countryCode string,
	addons []events.AddonSelection, couponCode string,
) (*subscriptionRecord, error) {
	count := 0
	if s.orgReader != nil {
		ownedIDs, err := s.orgReader.ListOwnedOrganizationIDs(ctx, createdBy)
		if err != nil {
			return nil, fmt.Errorf("billing.provisionSubscription: list owned organizations: %w", err)
		}
		count, err = s.repo.countSubscriptionsBySubjectIDs(ctx, s.querier(ctx), ownedIDs)
		if err != nil {
			return nil, fmt.Errorf("billing.provisionSubscription: count subscriptions: %w", err)
		}
	}

	currency := contracts.ResolveCurrency(countryCode)

	var sub *subscriptionRecord
	var planInfo *contracts.PlanInfo
	var subtotal int64
	var trialStarted *events.TrialStarted
	var invoiceCreated *events.InvoiceCreated

	// The DB-local writes below (subscription + history + invoice + line
	// items/charges) are wrapped in one transaction so a mid-sequence
	// failure can't leave a subscription with a partially-formed invoice.
	// Cross-module/external steps (payment-link creation, event publish)
	// stay outside — they're already best-effort/idempotent, and
	// publishing an event before its transaction commits would announce
	// state that might still roll back.
	err := db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		ctx := db.WithQuerier(ctx, tx)
		isNew := true

		var err error
		if count == 0 {
			trialEnd := time.Now().AddDate(0, 0, 7)
			sub, err = s.repo.insertSubscriptionWithTrial(ctx, s.querier(ctx),
				subjectType, subjectID, plan, cycle, currency, trialEnd)
		} else {
			sub, err = s.repo.insertSubscription(ctx, s.querier(ctx),
				subjectType, subjectID, plan, cycle, currency)
		}
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("billing.provisionSubscription: insert subscription: %w", err)
			}
			// Subscription already exists (idempotency: message redelivery or prior partial failure).
			// Fetch it and fall through — the invoice check below handles the missing-invoice case.
			isNew = false
			sub, err = s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
			if err != nil {
				return fmt.Errorf("billing.provisionSubscription: %w", err)
			}
		}

		// Seed the owner's own seat so "members" usage starts accurate
		// instead of missing: checkUsageLimit fails open to current=0 when
		// no billing.usage row exists yet, which would let the very first
		// invite/add past the plan's limit regardless of the owner already
		// occupying one seat. Only on first provisioning (isNew) — a
		// redelivery must not reset usage that's grown since the original
		// commit. periodStart/periodEnd mirror recordUsage's own derivation
		// so a later syncMemberUsage call upserts this same row.
		if isNew {
			periodStart := sub.CreatedAt
			periodEnd := sub.CreatedAt.AddDate(0, 1, 0)
			if sub.PeriodStart != nil {
				periodStart = *sub.PeriodStart
			}
			if sub.PeriodEnd != nil {
				periodEnd = *sub.PeriodEnd
			}
			if err := s.repo.upsertUsage(ctx, s.querier(ctx), subjectID, "members", 1, periodStart, periodEnd); err != nil {
				return fmt.Errorf("billing.provisionSubscription: seed usage: %w", err)
			}
		}

		// Attach the cart's addons/coupon before any invoice is composed
		// below — both organization.createOrganization's up-front
		// validation already confirmed these are valid, so this is pure
		// attachment. Runs unconditionally (not gated on isNew): on a
		// redelivery after a prior successful commit, upsertSubscriptionAddon
		// is a plain idempotent upsert, and the coupon redemption is
		// explicitly guarded against being inserted twice below.
		for _, a := range addons {
			_ = s.repo.upsertSubscriptionAddon(ctx, s.querier(ctx), sub.ID, a.AddonID, a.Quantity)
		}
		if couponCode != "" {
			redemptions, _ := s.repo.listCouponRedemptionsForSubscription(ctx, s.querier(ctx), sub.ID)
			alreadyRedeemed := false
			for _, r := range redemptions {
				if r.CouponCode == couponCode {
					alreadyRedeemed = true
					break
				}
			}
			if !alreadyRedeemed {
				if ok, _ := s.repo.tryIncrementCouponRedeemedCount(ctx, s.querier(ctx), couponCode); ok {
					_ = s.repo.insertCouponRedemption(ctx, s.querier(ctx), couponCode, sub.ID)
				}
			}
		}

		planInfo, _ = s.planCatalog(ctx, plan)
		if planInfo != nil {
			subtotal = planInfo.Price(currency, sub.Cycle)
		}

		if isNew {
			action := "activate"
			if sub.Status == statusTrialing {
				action = "trial"
				if sub.TrialEnd != nil {
					trialStarted = &events.TrialStarted{OrgID: subjectID, Plan: plan, TrialEnd: *sub.TrialEnd}
				}
			}
			_, _ = s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, action,
				nil, &plan, subtotal, currency, createdBy, nil)
		}

		if sub.Status == statusActive && subtotal > 0 && planInfo != nil {
			// Idempotent: skip if a pending invoice already exists (handles retries and redeliveries).
			hasPending, _ := s.repo.hasPendingInvoice(ctx, s.querier(ctx), sub.ID)
			if !hasPending {
				composed, addonLines, couponCode, discountCents := s.composeInvoiceAmount(
					ctx, s.querier(ctx), sub.ID, planInfo, currency, sub.Cycle)
				taxRate := 0
				if s.taxReader != nil {
					taxRate, _ = s.taxReader.GetCountryTaxRate(ctx, countryCode)
				}
				tax := calculateTax(composed, taxRate)
				inv, invErr := s.repo.insertInvoice(ctx, s.querier(ctx), subjectID, sub.ID, composed, taxRate, tax, currency, "activation", false)
				if invErr != nil {
					return fmt.Errorf("billing.provisionSubscription: insert invoice: %w", invErr)
				}
				if err := s.insertPlanLineItem(ctx, inv, planInfo); err != nil {
					return fmt.Errorf("billing.provisionSubscription: insert plan line item: %w", err)
				}
				if err := s.applyInvoiceCharges(ctx, s.querier(ctx), sub.ID, inv.ID, currency, addonLines, couponCode, discountCents); err != nil {
					return fmt.Errorf("billing.provisionSubscription: apply invoice charges: %w", err)
				}
				invoiceCreated = &events.InvoiceCreated{
					OrgID: subjectID, InvoiceID: inv.ID,
					Plan: plan, AmountCents: inv.AmountCents, Currency: currency, DueAt: *inv.DueAt,
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Always (re-)schedule; duplicate delayed messages are harmless — downstream checks are idempotent.
	s.scheduleRenewalSequence(ctx, sub)

	if trialStarted != nil {
		events.Publish(ctx, s.pub, events.ExchangeBilling, events.RoutingKeyTrialStarted, "billing", subjectID, *trialStarted)
	}
	if invoiceCreated != nil {
		// Payment link creation is best-effort; the Pay button always creates one on demand.
		_, _ = s.createPaymentLink(ctx, "", "", invoiceCreated.InvoiceID)
		events.Publish(ctx, s.pub, events.ExchangeBilling, events.RoutingKeyInvoiceCreated, "billing", subjectID, *invoiceCreated)
	}

	return sub, nil
}

// getSubscription is also exposed cross-module as contracts.BillingReader's
// GetSubscriptionBySubject — every non-handler caller only checks err == nil,
// so classifying here is safe (and errors.Is against the wrapped cause, e.g.
// pgx.ErrNoRows, still works via apperr.Error.Unwrap).
func (s *service) getSubscription(ctx context.Context, subjectType, subjectID string) (*subscriptionRecord, error) {
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("SUBSCRIPTION_NOT_FOUND", "subscription not found", err)
		}
		return nil, apperr.Internal("SUBSCRIPTION_FETCH_FAILED", "failed to get subscription", err)
	}
	return sub, nil
}

// Bare repo-call returns below are deliberate: each repo function already
// self-prefixes (e.g. "billing.findSubscriptionBySubject: ..."), and every
// path funnels into this function's single deferred apperr classification —
// same tradeoff as redeemCoupon (service_coupon.go).
func (s *service) changePlanWithMetadata(
	ctx context.Context, subjectType, subjectID, plan, cycle, changedBy string, metadata []byte,
) (historyID string, sub *subscriptionRecord, err error) {
	defer func() {
		if err == nil {
			return
		}
		sub = nil
		historyID = ""
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			err = apperr.NotFound("SUBSCRIPTION_NOT_FOUND", "subscription not found", err)
		case errors.Is(err, ErrUnknownPlan):
			err = apperr.Validation("UNKNOWN_PLAN", "unknown plan")
		case errors.Is(err, ErrPlanChangeNotAllowed):
			err = apperr.Validation("PLAN_CHANGE_NOT_ALLOWED", "subscription cannot change plans in its current state")
		default:
			err = apperr.Internal("PLAN_CHANGE_FAILED", "failed to change plan", err)
		}
	}()

	newPlanInfo, err := s.planCatalog(ctx, plan)
	if err != nil {
		return "", nil, ErrUnknownPlan
	}

	sub, err = s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
	if err != nil {
		return "", nil, err
	}
	// A cancelled sub has no active billing to change — resume it first.
	// Every other status (active/trialing/expired/past_due) is allowed:
	// trialing has no invoice yet, expired/past_due may want a different
	// plan queued up before or right as they pay to reactivate.
	if sub.Status == statusCancelled {
		return "", nil, ErrPlanChangeNotAllowed
	}

	oldPlanInfo, err := s.planCatalog(ctx, sub.Plan)
	if err != nil {
		return "", nil, fmt.Errorf("billing.changePlan: %w: current plan", ErrUnknownPlan)
	}

	action := "upgrade"
	if newPlanInfo.SortOrder < oldPlanInfo.SortOrder {
		action = actionDowngrade
	}
	var updated *subscriptionRecord

	// Prorate: adjust period_end based on price ratio using the new cycle's price
	if sub.PeriodStart != nil && sub.PeriodEnd != nil && sub.Status != statusTrialing {
		oldPrice := int(oldPlanInfo.Price(sub.Currency, sub.Cycle))
		newPrice := int(newPlanInfo.Price(sub.Currency, cycle))
		newEnd := prorate(time.Now(), *sub.PeriodStart, *sub.PeriodEnd, oldPrice, newPrice, sub.Cycle, cycle)
		updated, err = s.repo.updateSubscriptionPlanAndPeriod(ctx, s.querier(ctx), sub.ID, plan, cycle, newEnd)
	} else {
		updated, err = s.repo.updateSubscriptionPlan(ctx, s.querier(ctx), sub.ID, plan, cycle)
	}
	if err != nil {
		return "", nil, err
	}

	historyID, err = s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, action,
		new(sub.Plan), &plan, 0, sub.Currency, changedBy, metadata)
	if err != nil {
		return "", nil, err
	}

	// Void any pending invoices from the previous plan and issue a fresh one
	// for the new plan + cycle so the user pays the correct amount.
	if hasPending, _ := s.repo.hasPendingInvoice(ctx, s.querier(ctx), updated.ID); hasPending {
		if err = s.repo.voidPendingInvoicesAndLinks(ctx, s.querier(ctx), updated.ID); err != nil {
			return "", nil, err
		}
		composed, addonLines, couponCode, discountCents := s.composeInvoiceAmount(
			ctx, s.querier(ctx), updated.ID, newPlanInfo, updated.Currency, cycle)
		if composed > 0 {
			taxRate := 0
			if s.taxReader != nil {
				taxRate, _ = s.taxReader.GetCountryTaxRate(ctx, countryFromCurrency(updated.Currency))
			}
			tax := calculateTax(composed, taxRate)
			inv, invErr := s.repo.insertInvoice(
				ctx, s.querier(ctx), subjectID, updated.ID, composed, taxRate, tax, updated.Currency, "subscription", false,
			)
			if invErr != nil {
				return "", nil, invErr
			}
			if invErr := s.insertPlanLineItem(ctx, inv, newPlanInfo); invErr != nil {
				return "", nil, invErr
			}
			if invErr := s.applyInvoiceCharges(ctx, s.querier(ctx), updated.ID, inv.ID,
				updated.Currency, addonLines, couponCode, discountCents); invErr != nil {
				return "", nil, invErr
			}
		}
	}

	s.publishAfterCommit(ctx, events.RoutingKeySubscriptionActivated, subjectID,
		events.SubscriptionActivated{
			OrgID:          subjectID,
			SubscriptionID: updated.ID,
			Plan:           updated.Plan,
			ActivatedAt:    updated.UpdatedAt,
		})
	return historyID, updated, nil
}

// Same deliberate no-wrap tradeoff as changePlanWithMetadata above.
func (s *service) cancelSubscription(
	ctx context.Context, subjectType, subjectID, cancelledBy, reason, details string,
) (sub *subscriptionRecord, err error) {
	defer func() {
		if err == nil {
			return
		}
		sub = nil
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			err = apperr.NotFound("SUBSCRIPTION_NOT_FOUND", "subscription not found", err)
		case errors.Is(err, ErrSubscriptionNotCancellable):
			err = apperr.Validation("SUBSCRIPTION_NOT_CANCELLABLE", "subscription cannot be cancelled in its current state")
		default:
			err = apperr.Internal("SUBSCRIPTION_CANCEL_FAILED", "failed to cancel subscription", err)
		}
	}()

	sub, err = s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
	if err != nil {
		return nil, err
	}

	if sub.Status == statusCancelled || sub.Status == statusExpired {
		return nil, ErrSubscriptionNotCancellable
	}

	// Trialing has no paid period to protect, so cancellation applies
	// immediately below. Every other status defers to renewal instead:
	// status stays whatever it was (active/past_due) until the renewal
	// worker actually terminates the subscription — never reduce
	// entitlement mid-period. Scheduling a cancellation also supersedes
	// every other scheduled amendment, since it's the maximal reduction.
	if sub.Status != statusTrialing {
		if err = s.repo.lockSubscriptionForUpdate(ctx, s.querier(ctx), sub.ID); err != nil {
			return nil, err
		}
		if err = s.repo.scheduleCancellation(ctx, s.querier(ctx), sub.ID); err != nil {
			return nil, err
		}
		if err = s.repo.clearScheduledPlanDowngrade(ctx, s.querier(ctx), sub.ID); err != nil {
			return nil, err
		}
		if err = s.repo.clearAllScheduledAddonQuantityChanges(ctx, s.querier(ctx), sub.ID); err != nil {
			return nil, err
		}
		metadata, _ := json.Marshal(struct {
			Reason  string `json:"reason"`
			Details string `json:"details,omitempty"`
		}{Reason: reason, Details: details})
		phase := historyPhaseScheduled
		if _, err = s.repo.insertHistoryWithPhase(ctx, s.querier(ctx), sub.ID, "cancel",
			&sub.Plan, nil, 0, sub.Currency, cancelledBy, metadata, &phase, sub.PeriodEnd); err != nil {
			return nil, err
		}
		// Deliberately no status change, no SubscriptionCancelled publish —
		// that fires only when the renewal worker actually applies this.
		sub, err = s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
		if err != nil {
			return nil, err
		}
		return sub, nil
	}

	updated, err := s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, statusCancelled)
	if err != nil {
		return nil, err
	}

	metadata, _ := json.Marshal(struct {
		Reason  string `json:"reason"`
		Details string `json:"details,omitempty"`
	}{Reason: reason, Details: details})
	_, _ = s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "cancel",
		&sub.Plan, nil, 0, sub.Currency, cancelledBy, metadata)

	s.publishAfterCommit(ctx, events.RoutingKeySubscriptionCancelled, subjectID,
		events.SubscriptionCancelled{
			OrgID:          subjectID,
			SubscriptionID: sub.ID,
			CancelledAt:    time.Now(),
		})
	return updated, nil
}

// undoScheduledCancellation clears a scheduled cancellation while status is
// still active/past_due — the subscription never actually stopped. Distinct
// from resumeSubscription, which only reactivates an already-cancelled
// subscription with a brand-new billing period; this requires
// scheduled_cancel_at set regardless of status, and a subscription that has
// already been cancelled has nothing left to un-schedule.
func (s *service) undoScheduledCancellation(
	ctx context.Context, subjectType, subjectID string,
) (sub *subscriptionRecord, err error) {
	defer func() {
		if err == nil {
			return
		}
		sub = nil
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			err = apperr.NotFound("SUBSCRIPTION_NOT_FOUND", "subscription not found", err)
		case errors.Is(err, ErrNoScheduledCancellation):
			err = apperr.Validation("NO_SCHEDULED_CANCELLATION", "no scheduled cancellation to undo")
		default:
			err = apperr.Internal("SUBSCRIPTION_UNDO_CANCEL_FAILED", "failed to undo scheduled cancellation", err)
		}
	}()

	sub, err = s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	if err = s.repo.lockSubscriptionForUpdate(ctx, s.querier(ctx), sub.ID); err != nil {
		return nil, err
	}
	if sub.Status == statusCancelled || sub.ScheduledCancelAt == nil {
		return nil, ErrNoScheduledCancellation
	}
	if err = s.repo.clearScheduledCancellation(ctx, s.querier(ctx), sub.ID); err != nil {
		return nil, err
	}
	sub, err = s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	return sub, nil
}

func (s *service) expireIfDue(ctx context.Context, subscriptionID string) error {
	sub, err := s.repo.findSubscriptionByID(ctx, s.querier(ctx), subscriptionID)
	if err != nil {
		return fmt.Errorf("billing.expireIfDue: %w", err)
	}

	if sub.Status != statusActive && sub.Status != statusTrialing {
		return nil
	}

	now := time.Now()
	expired := false
	if sub.TrialEnd != nil && now.After(*sub.TrialEnd) {
		expired = true
	} else if sub.PeriodEnd != nil && now.After(*sub.PeriodEnd) {
		expired = true
	}
	if !expired {
		return nil
	}

	if _, err := s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, statusExpired); err != nil {
		return fmt.Errorf("billing.expireIfDue: %w", err)
	}

	_, _ = s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "expire", &sub.Plan,
		nil, 0, sub.Currency, changedBySystem, nil)

	if s.orgSuspender != nil && sub.SubjectType == subjectTypeOrganization {
		_ = s.orgSuspender.SuspendOrganization(ctx, sub.SubjectID, "subscription expired")
	}

	events.Publish(ctx, s.pub, events.ExchangeBilling, events.RoutingKeySubscriptionExpired, "billing", sub.SubjectID,
		events.SubscriptionExpired{OrgID: sub.SubjectID, SubscriptionID: sub.ID, ExpiredAt: now})

	return nil
}

// cancelOnDeletion cancels the organization subscription when the organization is deleted.
// No event is published — the organization is already gone.
func (s *service) cancelOnDeletion(ctx context.Context, organizationID string) error {
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectTypeOrganization, organizationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("billing.cancelOnDeletion: %w", err)
	}
	if sub.Status == statusCancelled || sub.Status == statusExpired {
		return nil
	}
	if _, err := s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, statusCancelled); err != nil {
		return fmt.Errorf("billing.cancelOnDeletion: %w", err)
	}
	_, _ = s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "cancel",
		&sub.Plan, nil, 0, sub.Currency, changedBySystem, nil)
	return nil
}
