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
	var invoiceCreated *events.InvoiceCreated

	// The DB-local writes below (subscription + history + invoice + line
	// items/charges) are wrapped in one transaction so a mid-sequence
	// failure can't leave a subscription with a partially-formed invoice.
	// The event enqueues for whatever this provisioning produced are
	// enqueued inside this same transaction (see below), so they commit or
	// roll back atomically with the state they describe. Only the
	// cross-module/external step (payment-link creation) stays outside —
	// it's already best-effort/idempotent and makes a blocking provider
	// HTTP call this transaction must not hold a connection open across.
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
			// Subscription already exists: a redelivery. Everything here commits as one
			// transaction, so its first invoice was settled by the delivery that created
			// it. Issuing again would bill a paid or coupon-covered activation a second time.
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
		if err := s.attachCartSelections(ctx, s.querier(ctx), sub.ID, addons, couponCode); err != nil {
			return err
		}

		// planCatalog reads via s.pool, not the tx-scoped querier — unlike
		// every other call in this transaction, a failure here isn't caught
		// by Postgres aborting the transaction, so it must be propagated
		// explicitly: a swallowed error here used to leave subtotal at 0,
		// silently skipping invoice creation below for what should have
		// been a billed 2nd+ organization.
		planInfo, err = s.planCatalog(ctx, plan)
		if err != nil {
			return fmt.Errorf("billing.provisionSubscription: plan catalog: %w", err)
		}
		subtotal = planInfo.Price(currency, sub.Cycle)

		if isNew {
			action := "activate"
			var trialStarted *events.TrialStarted
			if sub.Status == statusTrialing {
				action = "trial"
				if sub.TrialEnd != nil {
					trialStarted = &events.TrialStarted{OrgID: subjectID, Plan: plan, TrialEnd: *sub.TrialEnd}
				}
			}
			if _, err := s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, action,
				nil, &plan, subtotal, currency, createdBy, nil); err != nil {
				return fmt.Errorf("billing.provisionSubscription: insert history: %w", err)
			}
			if trialStarted != nil {
				if err := events.Enqueue(ctx, s.querier(ctx), events.ExchangeBilling,
					events.RoutingKeyTrialStarted, "billing", subjectID, *trialStarted); err != nil {
					return fmt.Errorf("billing.provisionSubscription: enqueue trial started: %w", err)
				}
			}
		}

		if isNew && sub.Status == statusActive && subtotal > 0 {
			created, err := s.composeAndInsertActivationInvoice(ctx,
				s.querier(ctx), subjectID, sub, planInfo, plan, currency, countryCode)
			if err != nil {
				return err
			}
			invoiceCreated = created
			if created != nil {
				if err := events.Enqueue(ctx, s.querier(ctx), events.ExchangeBilling,
					events.RoutingKeyInvoiceCreated, "billing", subjectID, *created); err != nil {
					return fmt.Errorf("billing.provisionSubscription: enqueue invoice created: %w", err)
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

	if invoiceCreated != nil {
		// Payment link creation is best-effort; the Pay button always creates
		// one on demand. Deliberately after the transaction above (and its
		// event enqueues) already committed — a blocking provider HTTP call
		// must never hold that transaction's connection open.
		_, _ = s.createPaymentLink(ctx, "", "", invoiceCreated.InvoiceID)
	}

	return sub, nil
}

// attachCartSelections attaches provisionSubscription's cart addons/coupon —
// organization.createOrganization's up-front validation already confirmed
// these are valid, so this is pure attachment. Runs unconditionally
// regardless of whether the subscription itself is new: on a redelivery
// after a prior successful commit, upsertSubscriptionAddon is a plain
// idempotent upsert, and the coupon redemption is explicitly guarded
// against being inserted twice.
func (s *service) attachCartSelections(
	ctx context.Context, q db.Querier, subscriptionID string,
	addons []events.AddonSelection, couponCode string,
) error {
	for _, a := range addons {
		if err := s.repo.upsertSubscriptionAddon(ctx, q, subscriptionID, a.AddonID, a.Quantity); err != nil {
			return fmt.Errorf("billing.attachCartSelections: attach cart addon: %w", err)
		}
	}
	if couponCode == "" {
		return nil
	}
	if err := s.repo.lockSubscriptionForUpdate(ctx, q, subscriptionID); err != nil {
		return fmt.Errorf("billing.attachCartSelections: %w", err)
	}
	err := s.takeCouponRedemption(ctx, q, subscriptionID, couponCode)
	switch {
	case errors.Is(err, errCouponAlreadyRedeemed):
		return nil // a redelivery: this subscription already has it
	case errors.Is(err, ErrCouponNotRedeemable):
		// Deliberate fail-loud: the coupon was valid when the org-creation
		// request was accepted but got exhausted by someone else before this
		// event was processed. This runs off the organization.created
		// consumer (consumers.go), strictly after the HTTP response already
		// reported success, so failing here can't stop org creation — it
		// rolls back subscription provisioning instead. The consumer retries
		// (MaxDeliveries: 5) then dead-letters, since exhaustion isn't
		// transient; the organization is left without a subscription until
		// someone drains billing.events.dlq. No DLQ alerting exists yet —
		// accepted tradeoff over silently dropping the discount.
		return fmt.Errorf("billing.attachCartSelections: %w", err)
	case err != nil:
		return fmt.Errorf("billing.attachCartSelections: redeem coupon: %w", err)
	}
	return nil
}

// composeAndInsertActivationInvoice creates and charges an "activation" invoice for
// a 2nd+ organization's subscription, which just became active with something to
// bill. When a coupon covers all of it there is nothing to invoice, and it returns
// (nil, nil) with the charge settled.
func (s *service) composeAndInsertActivationInvoice(
	ctx context.Context, q db.Querier, subjectID string,
	sub *subscriptionRecord, planInfo *contracts.PlanInfo,
	plan, currency, countryCode string,
) (*events.InvoiceCreated, error) {
	composed, addonLines, couponCode, discountCents, err := s.composeInvoiceAmount(
		ctx, q, sub.ID, planInfo, currency, sub.Cycle, nil)
	if err != nil {
		return nil, fmt.Errorf("billing.composeAndInsertActivationInvoice: compose invoice amount: %w", err)
	}
	if composed <= 0 {
		if _, err := s.settleCoveredCharge(ctx, sub, invoiceKindActivation, couponCode, changedBySystem); err != nil {
			return nil, fmt.Errorf("billing.composeAndInsertActivationInvoice: %w", err)
		}
		return nil, nil
	}
	taxRate := 0
	if s.taxReader != nil {
		taxRate, _ = s.taxReader.GetCountryTaxRate(ctx, countryCode)
	}
	tax := calculateTax(composed, taxRate)
	inv, err := s.repo.insertInvoice(ctx, q, subjectID, sub.ID, composed, taxRate, tax, currency, "activation", false, nil)
	if err != nil {
		return nil, fmt.Errorf("billing.composeAndInsertActivationInvoice: insert invoice: %w", err)
	}
	if err := s.insertPlanLineItem(ctx, inv, planInfo, sub.Cycle); err != nil {
		return nil, fmt.Errorf("billing.composeAndInsertActivationInvoice: insert plan line item: %w", err)
	}
	if err := s.applyInvoiceCharges(ctx, q, sub.ID, inv.ID, currency, addonLines, couponCode, discountCents); err != nil {
		return nil, fmt.Errorf("billing.composeAndInsertActivationInvoice: apply invoice charges: %w", err)
	}
	return &events.InvoiceCreated{
		OrgID: subjectID, InvoiceID: inv.ID,
		Plan: plan, AmountCents: inv.AmountCents, Currency: currency, DueAt: *inv.DueAt,
	}, nil
}

// issuePeriodInvoice issues sub's renewal or activation at its current plan, cycle and
// add-ons, after whatever it replaces has been voided. It returns nil when there is
// nothing to charge. A coupon covering an activation settles it here. A covered
// renewal is left to the auto-invoice, which rolls the period when it comes due and
// is the one to use the coupon, unless the subscription is past due: the auto-invoice
// ignores that status, and the renewal is already due, so it settles here as well. A
// plan with no price and no coupon owes nothing.
func (s *service) issuePeriodInvoice(
	ctx context.Context, sub *subscriptionRecord, planInfo *contracts.PlanInfo, kind string,
) (*invoiceRecord, error) {
	q := s.querier(ctx)
	// A renewal pays for the next period, so it is priced from what applies
	// then: a scheduled downgrade or add-on decrease takes effect at the boundary.
	cycle := sub.Cycle
	var quantities map[string]int
	if kind == invoiceKindSubscription {
		var err error
		if planInfo, cycle, quantities, err = s.futurePricing(ctx, q, sub); err != nil {
			return nil, fmt.Errorf("billing.issuePeriodInvoice: %w", err)
		}
	}
	composed, addonLines, couponCode, discountCents, err := s.composeInvoiceAmount(
		ctx, q, sub.ID, planInfo, sub.Currency, cycle, quantities)
	if err != nil {
		return nil, fmt.Errorf("billing.issuePeriodInvoice: %w", err)
	}
	if composed <= 0 {
		switch {
		case couponCode == "":
			return nil, nil
		case kind == invoiceKindSubscription && sub.Status != statusPastDue:
			return nil, s.enqueueAutoInvoice(ctx, sub)
		}
		outcome, err := s.settleCoveredCharge(ctx, sub, kind, couponCode, changedBySystem)
		if err != nil {
			return nil, fmt.Errorf("billing.issuePeriodInvoice: %w", err)
		}
		if outcome.scheduleRenewal != nil {
			s.scheduleRenewalSequence(ctx, outcome.scheduleRenewal)
		}
		return nil, nil
	}
	taxRate := 0
	if s.taxReader != nil {
		taxRate, _ = s.taxReader.GetCountryTaxRate(ctx, countryFromCurrency(sub.Currency))
	}
	inv, err := s.repo.insertInvoice(ctx, q, sub.SubjectID, sub.ID, composed,
		taxRate, calculateTax(composed, taxRate), sub.Currency, kind, false, nil)
	if err != nil {
		return nil, fmt.Errorf("billing.issuePeriodInvoice: %w", err)
	}
	if err := s.insertPlanLineItem(ctx, inv, planInfo, cycle); err != nil {
		return nil, fmt.Errorf("billing.issuePeriodInvoice: %w", err)
	}
	if err := s.applyInvoiceCharges(ctx, q, sub.ID, inv.ID, sub.Currency, addonLines, couponCode, discountCents); err != nil {
		return nil, fmt.Errorf("billing.issuePeriodInvoice: %w", err)
	}
	return inv, nil
}

// getSubscription is also exposed cross-module as contracts.BillingReader's
// GetSubscriptionBySubject — called either under an ambient transaction (a
// billing-route caller, already RLS-wrapped) or with no querier in context
// at all (organization's webhook/invitation/member paths, or the rate-limiter
// middleware, neither of which owns billing's pool or an RLS-scoped
// transaction). db.HasQuerier distinguishes the two, same split
// recordUsage already uses (service_usage.go) for identical reasons:
// billing.subscriptions has FORCE ROW LEVEL SECURITY, so a bare-pool read
// with no app.organization_id set is silently filtered to zero rows.
func (s *service) getSubscription(ctx context.Context, subjectType, subjectID string) (*subscriptionRecord, error) {
	if db.HasQuerier(ctx) {
		return s.getSubscriptionTx(ctx, subjectType, subjectID)
	}
	var sub *subscriptionRecord
	err := s.withOrgTx(ctx, subjectType, subjectID, func(tx db.Querier) error {
		var txErr error
		sub, txErr = s.getSubscriptionTx(db.WithQuerier(ctx, tx), subjectType, subjectID)
		return txErr
	})
	return sub, err
}

// getSubscriptionTx is getSubscription's body — every non-handler caller
// only checks err == nil, so classifying here is safe (and errors.Is against
// the wrapped cause, e.g. pgx.ErrNoRows, still works via apperr.Error.Unwrap).
func (s *service) getSubscriptionTx(ctx context.Context, subjectType, subjectID string) (*subscriptionRecord, error) {
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("SUBSCRIPTION_NOT_FOUND", "subscription not found", err)
		}
		return nil, apperr.Internal("SUBSCRIPTION_FETCH_FAILED", "failed to get subscription", err)
	}
	return sub, nil
}

// listPlansCatalog is the existing-subscription counterpart to
// reference.listPlans: an org's currency is already fixed at creation, so
// this scopes every plan's prices to sub.Currency directly rather than
// re-resolving currency by request IP the way the pre-org-creation catalog
// does.
func (s *service) listPlansCatalog(ctx context.Context, organizationID string) ([]contracts.PlanInfo, error) {
	sub, err := s.getSubscription(ctx, subjectTypeOrganization, organizationID)
	if err != nil {
		return nil, err
	}
	plans, err := s.plansCatalog(ctx)
	if err != nil {
		return nil, apperr.Internal("PLANS_CATALOG_FETCH_FAILED", "failed to list plans catalog", err)
	}
	for i := range plans {
		plans[i].Prices = scopeCatalogPrices(plans[i].Prices, sub.Currency)
	}
	return plans, nil
}

// changePlanForOwner is what an owner's PATCH goes through. A lower plan is refused on
// every status, because /downgrade serves every status and is what resolves the members
// over its limit. A plan that is not self-serve is refused because its price would not
// bill for what it grants, unless it is the plan the subscription already has: a customer
// an operator put on one can still move between its cycles. changePlanWithMetadata stays
// unguarded, since /downgrade reuses it on a trial. An unknown plan or a missing
// subscription is left for it to classify.
func (s *service) changePlanForOwner(
	ctx context.Context, subjectType, subjectID,
	plan, cycle, changedBy string, metadata []byte,
) (string, *subscriptionRecord, error) {
	sub, subErr := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
	newPlan, newErr := s.planCatalog(ctx, plan)
	if subErr == nil && newErr == nil && plan != sub.Plan {
		if !newPlan.SelfServe(sub.Currency, cycle) {
			return "", nil, apperr.Validation(planNotSelfServeCode, planNotSelfServeMsg)
		}
		if current, err := s.planCatalog(ctx, sub.Plan); err == nil && newPlan.SortOrder < current.SortOrder {
			return "", nil, apperr.Validation("PLAN_CHANGE_IS_DOWNGRADE", "a lower plan goes through the downgrade flow")
		}
	}
	return s.changePlanWithMetadata(ctx, subjectType, subjectID, plan, cycle, changedBy, metadata)
}

// Bare repo-call returns below are deliberate: each repo function already
// self-prefixes (e.g. "billing.findSubscriptionBySubject: ..."), and every
// path funnels into this function's single deferred apperr classification —
// same tradeoff as redeemCoupon (service_coupon.go).
func (s *service) changePlanWithMetadata(
	ctx context.Context, subjectType, subjectID,
	plan, cycle, changedBy string, metadata []byte,
) (historyID string, sub *subscriptionRecord, err error) {
	defer func() {
		if err == nil {
			return
		}
		sub = nil
		historyID = ""
		if _, ok := errors.AsType[*apperr.Error](err); ok {
			return
		}
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
	// Everything below prorates from the period read here and replaces what is pending,
	// so a payment in flight must commit first: its period would otherwise be overwritten.
	if err = s.repo.lockSubscriptionForUpdate(ctx, s.querier(ctx), sub.ID); err != nil {
		return "", nil, err
	}
	if sub, err = s.repo.findSubscriptionByID(ctx, s.querier(ctx), sub.ID); err != nil {
		return "", nil, err
	}
	// A cancelled sub has no active billing to change — resume it first.
	// Every other status (active/trialing/expired/past_due) is allowed:
	// trialing has no invoice yet, expired/past_due may want a different
	// plan queued up before or right as they pay to reactivate.
	if sub.Status == statusCancelled {
		return "", nil, ErrPlanChangeNotAllowed
	}
	// A plan change replaces a scheduled downgrade, which would otherwise undo
	// it at the boundary. One already paid for cannot be replaced.
	if scheduleLocked(sub) {
		return "", nil, errAmendmentAlreadyPaid()
	}
	if sub.ScheduledPlan != nil {
		if err = s.repo.clearScheduledPlanDowngrade(ctx, s.querier(ctx), sub.ID); err != nil {
			return "", nil, err
		}
		if _, err = s.repo.syncScheduledEffectiveAt(ctx, s.querier(ctx), sub.ID); err != nil {
			return "", nil, err
		}
		if sub, err = s.repo.findSubscriptionByID(ctx, s.querier(ctx), sub.ID); err != nil {
			return "", nil, err
		}
	}

	oldPlanInfo, err := s.planCatalog(ctx, sub.Plan)
	if err != nil {
		return "", nil, fmt.Errorf("billing.changePlan: %w: current plan", ErrUnknownPlan)
	}

	action := "upgrade"
	if newPlanInfo.SortOrder < oldPlanInfo.SortOrder {
		action = actionDowngrade
	}
	pending, err := s.repo.pendingInvoiceKinds(ctx, s.querier(ctx), sub.ID)
	if err != nil {
		return "", nil, err
	}
	var updated *subscriptionRecord

	// planChangePeriod decides the new period. Prorating only happens for an
	// actual plan-tier change (plan != sub.Plan): a pure cycle switch on the
	// *same* plan isn't a different product with a different per-day value;
	// the yearly price is cheaper per day specifically to reward a real
	// 12-month upfront prepayment (the same reward extendSubscription's
	// switchToAnnual requires actual payment for). Running it through this
	// value-preserving proration would let that discount apply retroactively
	// to time already banked through unrelated means (e.g. extends),
	// manufacturing extra runway with no payment — period_end stays
	// untouched instead; only the cycle label (and future renewal pricing)
	// changes.
	oldPrice := int(oldPlanInfo.Price(sub.Currency, sub.Cycle))
	newPrice := int(newPlanInfo.Price(sub.Currency, cycle))
	if start, end := planChangePeriod(time.Now(), sub, pending[invoiceKindActivation], plan != sub.Plan,
		oldPrice, newPrice, cycle); end != nil {
		updated, err = s.repo.updateSubscriptionPlanAndPeriod(ctx, s.querier(ctx), sub.ID, plan, cycle, *start, *end)
		// An add-on schedule left in place follows the moved end.
		if err == nil {
			err = s.followPeriodEnd(ctx, sub, *end)
		}
	} else {
		updated, err = s.repo.updateSubscriptionPlan(ctx, s.querier(ctx), sub.ID, plan, cycle)
	}
	if err != nil {
		return "", nil, err
	}

	historyID, err = s.repo.insertHistoryWithCycle(ctx, s.querier(ctx), sub.ID, action,
		new(sub.Plan), &plan, 0, sub.Currency, changedBy, metadata,
		nil, nil, new(sub.Cycle), &cycle)
	if err != nil {
		return "", nil, err
	}

	// Every pending invoice was priced for the old plan and period: void
	// them all. A renewal or an unpaid activation is reissued for the new
	// plan + cycle with its kind kept — an activation backs the period it
	// was issued for, and reissuing it as a renewal would roll period_end a
	// full cycle forward once paid. An extension or addon increase is
	// dropped rather than reissued: both were priced against the old
	// period, so the owner requests them again; the addon rows stop waiting
	// on their voided invoices.
	if len(pending) > 0 {
		links, voidErr := s.repo.voidPendingInvoicesAndLinks(ctx, s.querier(ctx), updated.ID)
		if voidErr != nil {
			return "", nil, voidErr
		}
		if err = enqueueCheckoutExpiry(ctx, s.querier(ctx), subjectID, links); err != nil {
			return "", nil, err
		}
		if err = s.repo.clearPendingAddonIncreases(ctx, s.querier(ctx), updated.ID); err != nil {
			return "", nil, err
		}
	}
	if pending[invoiceKindActivation] || pending[invoiceKindSubscription] {
		kind := invoiceKindSubscription
		if pending[invoiceKindActivation] {
			kind = invoiceKindActivation
		}
		if _, err = s.issuePeriodInvoice(ctx, updated, newPlanInfo, kind); err != nil {
			return "", nil, err
		}
	}

	// The renewal messages queued for the old end now read as stale, so
	// queue the sequence for the new one. This runs inside the request
	// transaction: its outbox rows commit or roll back with the change.
	if updated.PeriodEnd != nil && (sub.PeriodEnd == nil || !updated.PeriodEnd.Equal(*sub.PeriodEnd)) {
		s.scheduleRenewalSequence(ctx, updated)
	}
	// A coupon covering the activation can have granted the longer cycle just now, and
	// scheduled that end itself. The answer must show it.
	if updated, err = s.repo.findSubscriptionByID(ctx, s.querier(ctx), updated.ID); err != nil {
		return "", nil, err
	}

	if err := s.enqueueEvent(ctx, events.RoutingKeySubscriptionActivated, subjectID,
		events.SubscriptionActivated{
			OrgID:          subjectID,
			SubscriptionID: updated.ID,
			Plan:           updated.Plan,
			ActivatedAt:    updated.UpdatedAt,
		}); err != nil {
		return "", nil, err
	}
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
		if _, ok := errors.AsType[*apperr.Error](err); ok {
			return
		}
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
	if err = s.repo.lockSubscriptionForUpdate(ctx, s.querier(ctx), sub.ID); err != nil {
		return nil, err
	}
	// Re-read under the lock: expireIfDue may have ended the subscription
	// between the read above and the lock.
	if sub, err = s.repo.findSubscriptionByID(ctx, s.querier(ctx), sub.ID); err != nil {
		return nil, err
	}
	if sub.Status == statusCancelled || sub.Status == statusExpired {
		return nil, ErrSubscriptionNotCancellable
	}

	// Cancellation never reduces entitlement before the period the owner
	// already has (or the trial) ends: status stays as it is until
	// expireIfDue applies the schedule at period_end/trial_end. Scheduling a
	// cancellation also supersedes every other scheduled amendment, since
	// it's the maximal reduction, and voids a renewal invoice already issued
	// for a period the owner no longer wants — undo reissues it.
	if err = s.repo.scheduleCancellation(ctx, s.querier(ctx), sub.ID); err != nil {
		return nil, err
	}
	// A schedule already paid for stays: the owner paid the scheduled price for
	// the period that ends with the cancellation.
	if !scheduleLocked(sub) {
		if err = s.repo.clearScheduledPlanDowngrade(ctx, s.querier(ctx), sub.ID); err != nil {
			return nil, err
		}
		if err = s.repo.clearAllScheduledAddonQuantityChanges(ctx, s.querier(ctx), sub.ID); err != nil {
			return nil, err
		}
		if _, err = s.repo.syncScheduledEffectiveAt(ctx, s.querier(ctx), sub.ID); err != nil {
			return nil, err
		}
	}
	links, err := s.repo.voidPendingInvoicesOfKinds(ctx, s.querier(ctx), sub.ID, []string{invoiceKindSubscription})
	if err != nil {
		return nil, err
	}
	if err = enqueueCheckoutExpiry(ctx, s.querier(ctx), sub.SubjectID, links); err != nil {
		return nil, err
	}
	metadata, _ := json.Marshal(struct {
		Reason  string `json:"reason"`
		Details string `json:"details,omitempty"`
	}{Reason: reason, Details: details})
	effectiveAt := sub.PeriodEnd
	if sub.Status == statusTrialing {
		effectiveAt = sub.TrialEnd
	}
	phase := historyPhaseScheduled
	if _, err = s.repo.insertHistoryWithPhase(ctx, s.querier(ctx), sub.ID, "cancel",
		&sub.Plan, nil, 0, sub.Currency, cancelledBy, metadata, &phase, effectiveAt); err != nil {
		return nil, err
	}
	// Deliberately no status change, no SubscriptionCancelled publish —
	// that fires only when expireIfDue actually applies this.
	return s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
}

// undoScheduledCancellation clears a scheduled cancellation while status is
// still active/trialing/past_due — the subscription never actually stopped.
// Distinct from resumeSubscription, which invoices an already-ended
// subscription's next period; this requires scheduled_cancel_at set, and a
// subscription that has already been cancelled has nothing left to
// un-schedule.
func (s *service) undoScheduledCancellation(
	ctx context.Context, subjectType, subjectID, changedBy string,
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
	if sub, err = s.repo.findSubscriptionByID(ctx, s.querier(ctx), sub.ID); err != nil {
		return nil, err
	}
	if sub.Status == statusCancelled || sub.ScheduledCancelAt == nil {
		return nil, ErrNoScheduledCancellation
	}
	if err = s.repo.clearScheduledCancellation(ctx, s.querier(ctx), sub.ID); err != nil {
		return nil, err
	}
	phase := historyPhaseUndone
	now := time.Now()
	if _, err = s.repo.insertHistoryWithPhase(ctx, s.querier(ctx), sub.ID, "cancel",
		&sub.Plan, nil, 0, sub.Currency, changedBy, nil, &phase, &now); err != nil {
		return nil, err
	}
	sub, err = s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	// The renewal invoice for the current period was either voided by the
	// cancellation or never issued (the auto-invoice skips a subscription
	// with a cancellation scheduled). A past_due renewal is already due and
	// the auto-invoice skips that status, so it is issued here; otherwise the
	// owner who kept the subscription would have nothing to pay before it
	// expires. Any other status queues the auto-invoice again. Only that
	// message: the reminder and expiry check already queued for this end are
	// still valid, and a second reminder would reach the owner twice.
	if sub.Status == statusPastDue {
		// A past_due activation is still pending (cancel voids renewals only), and
		// is what the owner pays; a renewal next to it would roll the period on an
		// unpaid first one.
		if _, err := s.repo.findPendingPeriodInvoice(ctx, s.querier(ctx), sub.ID); err == nil {
			return sub, nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err := s.issueRenewalAndNotify(ctx, sub); err != nil {
			return nil, err
		}
		return sub, nil
	}
	if err = s.enqueueAutoInvoice(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}

// expireIfDue is the single place a subscription ends. Once period_end
// (trial_end while trialing) has passed, it applies a scheduled
// cancellation — or, with none scheduled, expires the subscription — and
// suspends the organization. The row lock plus the re-read under it make a
// concurrently committed renewal win: that renewal moved period_end, so the
// due check no longer passes and nothing is overwritten. The suspension runs
// inside the transaction's closure, so a suspend failure rolls the status
// change back and the consumer redelivers, instead of leaving an ended
// subscription on an organization that still has access.
func (s *service) expireIfDue(ctx context.Context, subscriptionID string) error {
	return db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		ctx := db.WithQuerier(ctx, tx)
		q := s.querier(ctx)
		if err := s.repo.lockSubscriptionForUpdate(ctx, q, subscriptionID); err != nil {
			return fmt.Errorf("billing.expireIfDue: %w", err)
		}
		sub, err := s.repo.findSubscriptionByID(ctx, q, subscriptionID)
		if err != nil {
			return fmt.Errorf("billing.expireIfDue: %w", err)
		}
		if sub.Status != statusActive && sub.Status != statusTrialing && sub.Status != statusPastDue {
			return nil
		}

		// A past_due subscription's own renewal payment failed; letting it
		// through here is what finally ends it (and suspends the org) at
		// period_end instead of leaving it delinquent forever.
		end := sub.PeriodEnd
		if sub.Status == statusTrialing {
			end = sub.TrialEnd
		}
		now := time.Now()
		if end == nil || !now.After(*end) {
			return nil
		}

		reason := suspendReasonExpired
		if sub.ScheduledCancelAt != nil {
			reason = suspendReasonCancelled
			if err := s.repo.updateSubscriptionStatus(ctx, q, sub.ID, statusCancelled); err != nil {
				return fmt.Errorf("billing.expireIfDue: %w", err)
			}
			// The schedule is consumed here: a subscription resumed later
			// must not carry it into its next period.
			if err := s.repo.clearScheduledCancellation(ctx, q, sub.ID); err != nil {
				return fmt.Errorf("billing.expireIfDue: %w", err)
			}
			phase := historyPhaseApplied
			if _, err := s.repo.insertHistoryWithPhase(ctx, q, sub.ID, "cancel",
				&sub.Plan, nil, 0, sub.Currency, changedBySystem, nil, &phase, &now); err != nil {
				return fmt.Errorf("billing.expireIfDue: insert history: %w", err)
			}
			if err := events.Enqueue(ctx, q, events.ExchangeBilling, events.RoutingKeySubscriptionCancelled, "billing", sub.SubjectID,
				events.SubscriptionCancelled{OrgID: sub.SubjectID, SubscriptionID: sub.ID, CancelledAt: now}); err != nil {
				return fmt.Errorf("billing.expireIfDue: enqueue: %w", err)
			}
		} else {
			if err := s.repo.updateSubscriptionStatus(ctx, q, sub.ID, statusExpired); err != nil {
				return fmt.Errorf("billing.expireIfDue: %w", err)
			}
			if _, err := s.repo.insertHistory(ctx, q, sub.ID, "expire", &sub.Plan,
				nil, 0, sub.Currency, changedBySystem, nil); err != nil {
				return fmt.Errorf("billing.expireIfDue: insert history: %w", err)
			}
			if err := events.Enqueue(ctx, q, events.ExchangeBilling, events.RoutingKeySubscriptionExpired, "billing", sub.SubjectID,
				events.SubscriptionExpired{OrgID: sub.SubjectID, SubscriptionID: sub.ID, ExpiredAt: now}); err != nil {
				return fmt.Errorf("billing.expireIfDue: enqueue: %w", err)
			}
			// The boundary a schedule waited for has come: it applies to the
			// expired subscription, so a later Resume is priced and runs on it.
			if err := s.enqueueApplyScheduled(ctx, sub); err != nil {
				return fmt.Errorf("billing.expireIfDue: %w", err)
			}
		}

		// The subscription is over, so its unpaid invoices are over: mark
		// every still-pending invoice failed and its links expired, in this
		// same transaction. Deliberately emits no per-invoice event — the
		// SubscriptionExpired/SubscriptionCancelled above already tells every
		// consumer the subscription is over; a billing.invoice.failed
		// alongside it would trigger a fresh "payment failed" notification
		// and dunning cycle for a subscription that no longer exists.
		links, err := s.repo.failPendingInvoicesAndLinks(ctx, q, sub.ID)
		if err != nil {
			return fmt.Errorf("billing.expireIfDue: fail pending invoices: %w", err)
		}
		if err := enqueueCheckoutExpiry(ctx, q, sub.SubjectID, links); err != nil {
			return fmt.Errorf("billing.expireIfDue: %w", err)
		}
		if s.orgSuspender != nil && sub.SubjectType == subjectTypeOrganization {
			if err := s.orgSuspender.SuspendOrganization(ctx, sub.SubjectID, reason); err != nil {
				return fmt.Errorf("billing.expireIfDue: suspend organization: %w", err)
			}
		}
		return nil
	})
}

// cancelOnDeletion cancels the organization subscription when the organization is deleted.
// No event is published — the organization is already gone. An ended subscription keeps its
// status, but its open invoices still close: a Resume invoice paid after the deletion would
// otherwise bring the deleted organization's subscription back.
func (s *service) cancelOnDeletion(ctx context.Context, organizationID string) error {
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectTypeOrganization, organizationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("billing.cancelOnDeletion: %w", err)
	}
	ended := sub.Status == statusCancelled || sub.Status == statusExpired
	return db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		ctx := db.WithQuerier(ctx, tx)
		if !ended {
			if err := s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, statusCancelled); err != nil {
				return fmt.Errorf("billing.cancelOnDeletion: %w", err)
			}
			if _, err := s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "cancel",
				&sub.Plan, nil, 0, sub.Currency, changedBySystem, nil); err != nil {
				return fmt.Errorf("billing.cancelOnDeletion: insert history: %w", err)
			}
		}
		// Nobody can pay a deleted organization's open invoices any more:
		// fail them and close their links, here and at the provider, as an
		// expiry does. A payment that still arrives is recorded by the paid
		// webhook as unapplied rather than applied.
		links, err := s.repo.failPendingInvoicesAndLinks(ctx, s.querier(ctx), sub.ID)
		if err != nil {
			return fmt.Errorf("billing.cancelOnDeletion: fail pending invoices: %w", err)
		}
		return enqueueCheckoutExpiry(ctx, s.querier(ctx), organizationID, links)
	})
}
