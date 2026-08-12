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

const (
	addonAttachFailedCode = "ADDON_ATTACH_FAILED"
	addonAttachFailedMsg  = "failed to attach addon"
	addonDetachFailedCode = "ADDON_DETACH_FAILED"
	addonDetachFailedMsg  = "failed to detach addon"
)

// addonChangeMetadata is the subscription_history.metadata shape every
// "addon_change" history row carries — addon_id plus the quantity change,
// deliberately no addon name (the worker's renewal-reconciliation loop must
// stay catalog-lookup-free; the frontend resolves addon_id -> name from the
// addons catalog it already has loaded wherever history renders).
type addonChangeMetadata struct {
	AddonID      string `json:"addon_id"`
	FromQuantity int    `json:"from_quantity"`
	ToQuantity   int    `json:"to_quantity"`
	// Pending marks the one addon_change row that doesn't mean "quantity
	// changed to ToQuantity" the way every other row does: requestAddonIncrease's
	// invoice-creation row records a quantity increase that's only requested,
	// gated on that invoice's payment — the live quantity is still FromQuantity
	// until handleWebhook confirms paid. Omitted (false) for every other
	// addon_change row, where the quantity change already happened or is a
	// renewal-scheduled certainty.
	Pending bool `json:"pending,omitempty"`
}

// insertAddonChangeHistory writes one "addon_change" subscription_history
// row — shared by every addon lifecycle call site (attach/increase/decrease,
// detach, undo, and the renewal worker's scheduled-addon loop) so the
// metadata shape and action value can't drift between them. q is the
// caller's own querier so this participates in whatever transaction (if
// any) the caller is already inside.
func (s *service) insertAddonChangeHistory(
	ctx context.Context, q db.Querier, sub *subscriptionRecord, addonID string,
	fromQuantity, toQuantity int, changedBy string,
	phase *string, effectiveAt *time.Time, pending bool,
) error {
	metadata, _ := json.Marshal(addonChangeMetadata{
		AddonID: addonID, FromQuantity: fromQuantity, ToQuantity: toQuantity, Pending: pending,
	})
	if _, err := s.repo.insertHistoryWithPhase(ctx, q, sub.ID, "addon_change",
		nil, nil, 0, sub.Currency, changedBy, metadata, phase, effectiveAt); err != nil {
		return fmt.Errorf("billing.insertAddonChangeHistory: %w", err)
	}
	return nil
}

// scopeAddonPrice trims a's full multi-currency Prices map down to the
// subscription's own currency — the same "never show a currency the caller
// can't be charged in" rule reference.scopedPrices enforces for the catalog
// browsing endpoints (GET /references/plans|addons). This is a different,
// billing-owned endpoint (GET/POST/DELETE .../billing/addons) that leaked
// the same way and wasn't caught by that earlier pass. currency is always
// the subscription's own already-resolved sub.Currency here, never GeoIP —
// an existing org's currency is fixed at creation and must never silently
// drift based on where its owner currently is to ensure predictable billing.
// Falls back to USD if that currency has no entry, matching
// contracts.ResolveCurrency's own fallback semantics — never the full map.
func scopeAddonPrice(a *attachedAddonRecord, currency string) *attachedAddonRecord {
	price, ok := a.Prices[currency]
	if !ok {
		price, ok = a.Prices[contracts.CurrencyUSD]
		currency = contracts.CurrencyUSD
	}
	if !ok {
		a.Prices = map[string]contracts.PlanPrices{}
		return a
	}
	a.Prices = map[string]contracts.PlanPrices{currency: price}
	return a
}

// attachAddon attaches a new addon, or changes the quantity of one already
// attached. Trialing subscriptions apply every change immediately, matching
// today's behavior — trials have no paid period to protect and no revenue
// at stake either way. Active/past_due/expired subscriptions apply a
// decrease by deferring it to renewal (never reducing entitlement
// mid-period), same as before, but an increase — a brand-new attach or a
// further bump on one already paid for — no longer applies immediately:
// it gates the higher quantity on a day-prorated invoice's payment
// (requestAddonIncrease), closing the revenue-collection gap where a raised
// limit used to take effect with nothing billed until whatever invoice
// happened next, up to a full cycle away.
//
// The lock-through-decision sequence runs inside its own transaction rather
// than the group-level RLS transaction attachAddon's route used to run
// under (module.go moved this route to the RLS-free group): the increase
// path's payment-link creation is a blocking provider HTTP call, and the
// existing convention (extendSubscription, activateTrialNow) is to never
// hold a pooled DB connection open across one — see requestAddonIncrease.
func (s *service) attachAddon(ctx context.Context, organizationID, addonID string, quantity int, changedBy string) (*attachedAddonRecord, error) {
	addonInfo, err := s.addonCatalog(ctx, addonID)
	if err != nil {
		return nil, apperr.NotFound("ADDON_NOT_FOUND", "addon not found", ErrAddonNotFound)
	}
	var sub *subscriptionRecord
	err = s.withOrgTx(ctx, subjectTypeOrganization, organizationID, func(tx db.Querier) error {
		txCtx := db.WithQuerier(ctx, tx)
		var findErr error
		sub, findErr = s.repo.findSubscriptionBySubject(txCtx, tx, subjectTypeOrganization, organizationID)
		return findErr
	})
	if err != nil {
		return nil, apperr.Internal(addonAttachFailedCode, addonAttachFailedMsg, err)
	}

	var pendingInvoice *invoiceRecord
	err = s.withOrgTx(ctx, subjectTypeOrganization, organizationID, func(tx db.Querier) error {
		txCtx := db.WithQuerier(ctx, tx)
		if err := s.repo.lockSubscriptionForUpdate(txCtx, tx, sub.ID); err != nil {
			return err
		}

		existing, findErr := s.repo.findAttachedAddon(txCtx, tx, sub.ID, addonID)
		existingQuantity, rowExists := 0, false
		switch {
		case findErr == nil:
			existingQuantity, rowExists = existing.Quantity, true
		case !errors.Is(findErr, pgx.ErrNoRows):
			return findErr
		}

		switch {
		case existingQuantity == 0 || quantity >= existingQuantity:
			if err := s.repo.clearScheduledAddonQuantityChange(txCtx, tx, sub.ID, addonID); err != nil {
				return err
			}
			if quantity == existingQuantity {
				return nil // no-op: nothing changed, nothing to bill (trialing or not)
			}
			if sub.Status == statusTrialing {
				if err := s.repo.upsertSubscriptionAddon(txCtx, tx, sub.ID, addonID, quantity); err != nil {
					return err
				}
				return s.insertAddonChangeHistory(txCtx, tx, sub, addonID, existingQuantity, quantity, changedBy, nil, nil, false)
			}
			// No ScheduledCancelAt guard here: an increase request has
			// always been allowed through regardless of a scheduled
			// cancellation — only the decrease branch below rejects it.
			if !rowExists {
				if err := s.repo.upsertSubscriptionAddon(txCtx, tx, sub.ID, addonID, 0); err != nil {
					return err
				}
			} else if existing.PendingInvoiceID != nil {
				// Void-and-reissue: a newer request supersedes whatever
				// invoice an earlier increase request left pending.
				if err := s.repo.voidInvoiceAndLinks(txCtx, tx, *existing.PendingInvoiceID); err != nil {
					return err
				}
				if err := s.repo.clearPendingAddonIncrease(txCtx, tx, sub.ID, addonID); err != nil {
					return err
				}
			}
			inv, err := s.requestAddonIncrease(txCtx, tx, sub, addonID, addonInfo, existingQuantity, quantity, changedBy)
			if err != nil {
				return err
			}
			pendingInvoice = inv
			return nil
		case sub.Status == statusTrialing:
			if err := s.repo.upsertSubscriptionAddon(txCtx, tx, sub.ID, addonID, quantity); err != nil {
				return err
			}
			return s.insertAddonChangeHistory(txCtx, tx, sub, addonID, existingQuantity, quantity, changedBy, nil, nil, false)
		default:
			if sub.ScheduledCancelAt != nil {
				return apperr.Validation(cancellationScheduledCode, cancellationScheduledMsg)
			}
			if err := s.repo.scheduleAddonQuantityChange(txCtx, tx, sub.ID, addonID, quantity); err != nil {
				return err
			}
			phase := historyPhaseScheduled
			return s.insertAddonChangeHistory(txCtx, tx, sub, addonID, existingQuantity, quantity, changedBy, &phase, sub.PeriodEnd, false)
		}
	})
	if err != nil {
		var appErr *apperr.Error
		if errors.As(err, &appErr) {
			return nil, err
		}
		return nil, apperr.Internal(addonAttachFailedCode, addonAttachFailedMsg, err)
	}

	if pendingInvoice != nil {
		// Payment link is a convenience, not load-bearing — a missing/
		// unconfigured provider shouldn't block the attach/increase itself;
		// the invoice is already committed and payable via the regular
		// POST .../invoices/:id/pay flow, same fail-open convention as
		// extendSubscription/activateTrialNow.
		_, _ = s.createPaymentLink(ctx, subjectTypeOrganization, organizationID, pendingInvoice.ID)
		events.Publish(ctx, s.pub, events.ExchangeBilling, events.RoutingKeyInvoiceCreated, "billing", organizationID,
			events.InvoiceCreated{
				OrgID: organizationID, InvoiceID: pendingInvoice.ID,
				Plan: sub.Plan, AmountCents: pendingInvoice.AmountCents, Currency: sub.Currency,
				DueAt: *pendingInvoice.DueAt,
			})
	}

	addon, err := s.repo.findAttachedAddon(ctx, s.querier(ctx), sub.ID, addonID)
	if err != nil {
		return nil, apperr.Internal(addonAttachFailedCode, addonAttachFailedMsg, err)
	}
	return scopeAddonPrice(addon, sub.Currency), nil
}

// requestAddonIncrease prices a quantity increase (delta = quantity -
// existingQuantity, existingQuantity 0 for a brand-new attach) for the days
// remaining in the subscription's current period, applies the subscription's
// active coupon redemption exactly like any other invoice
// (computeCouponDiscount/applyInvoiceCharges), and creates the gating
// invoice + line item — all inside the caller's transaction. The addon's
// live quantity is deliberately not touched here; it only moves once
// handleWebhook confirms this invoice paid (service_webhook.go).
func (s *service) requestAddonIncrease(
	ctx context.Context, tx db.Querier,
	sub *subscriptionRecord, addonID string, addonInfo *contracts.AddonInfo,
	existingQuantity, quantity int, changedBy string,
) (*invoiceRecord, error) {
	delta := quantity - existingQuantity

	// unitPrice must cover the addon's full current period, not one flat
	// cycle price — a subscription extended past one cycle (extendSubscription,
	// up to maxRunwayMonths) still has sub.Cycle == "monthly"/"yearly", but its
	// period can span many cycles' worth of time. Tiered the same way
	// computeExtensionSubtotal prices an extension itself (12-month blocks at
	// the yearly rate, remainder at the monthly rate) so a same-day increase
	// on a multi-cycle period isn't undercharged for the months the addon will
	// actually occupy before this period's own renewal bills it again.
	unitPrice := int64(0)
	if prices, ok := addonInfo.Prices[sub.Currency]; ok {
		months := periodMonths(*sub.PeriodStart, *sub.PeriodEnd)
		blocks, remainder := months/12, months%12
		unitPrice = int64(blocks)*int64(prices.Yearly) + int64(remainder)*int64(prices.Monthly)
	}

	preDiscount := computeAddonIncreaseProration(*sub.PeriodStart, time.Now(), *sub.PeriodEnd, delta, unitPrice)

	var couponCode string
	var discountCents int64
	redemption, err := s.findActiveCouponRedemption(ctx, tx, sub.ID)
	switch {
	case err == nil:
		discountCents = computeCouponDiscount(redemption.DiscountType,
			redemption.AmountCents, redemption.PercentOff, preDiscount)
		if discountCents > 0 {
			couponCode = redemption.CouponCode
		}
	case errors.Is(err, pgx.ErrNoRows):
		// No active coupon — expected steady state, nothing to apply.
	default:
		return nil, fmt.Errorf("billing.requestAddonIncrease: find active coupon redemption: %w", err)
	}
	subtotal := preDiscount - discountCents

	taxRate := 0
	if s.taxReader != nil {
		taxRate, _ = s.taxReader.GetCountryTaxRate(ctx, countryFromCurrency(sub.Currency))
	}
	tax := calculateTax(subtotal, taxRate)

	if subtotal+tax <= 0 {
		// Nothing left to actually charge — a request landing in the final
		// moments of the period (or fully absorbed by a coupon) prorates to
		// zero. billing.invoices requires amount_cents > 0, and there's
		// nothing to gate on payment for a $0 charge anyway: apply the
		// increase directly instead of creating a zero-amount invoice.
		if err := s.repo.upsertSubscriptionAddon(ctx, tx, sub.ID, addonID, quantity); err != nil {
			return nil, fmt.Errorf("billing.requestAddonIncrease: %w", err)
		}
		if err := s.insertAddonChangeHistory(ctx, tx, sub, addonID, existingQuantity, quantity, changedBy, nil, nil, false); err != nil {
			return nil, err
		}
		return nil, nil
	}

	inv, err := s.repo.insertInvoice(ctx, tx, sub.SubjectID, sub.ID, subtotal,
		taxRate, tax, sub.Currency, "addon_increase", false, nil)
	if err != nil {
		return nil, fmt.Errorf("billing.requestAddonIncrease: %w", err)
	}

	// unit_price_cents is a per-unit display rate derived from the prorated
	// total (floor-rounded) — total_cents stays the exact preDiscount amount
	// regardless, matching insertExtensionLineItems' "rounding only affects
	// the displayed rate, never the actual charge" convention.
	addonLines := []lineItemSpec{{
		Description:    addonInfo.Name + " (prorated increase)",
		Quantity:       delta,
		UnitPriceCents: preDiscount / int64(delta),
		TotalCents:     preDiscount,
	}}
	if err := s.applyInvoiceCharges(ctx, tx, sub.ID, inv.ID, sub.Currency, addonLines, couponCode, discountCents); err != nil {
		return nil, fmt.Errorf("billing.requestAddonIncrease: %w", err)
	}

	if err := s.repo.setPendingAddonIncrease(ctx, tx, sub.ID, addonID, quantity, inv.ID); err != nil {
		return nil, fmt.Errorf("billing.requestAddonIncrease: %w", err)
	}
	if err := s.insertAddonChangeHistory(ctx, tx, sub, addonID, existingQuantity, quantity, changedBy, nil, nil, true); err != nil {
		return nil, err
	}

	return inv, nil
}

// detachAddon removes an addon. Trialing subscriptions delete the row
// immediately, matching today's behavior. Active subscriptions schedule the
// removal (quantity 0 at renewal) instead, so the live quantity keeps being
// read by checkUsageLimit until the renewal worker applies it.
func (s *service) detachAddon(ctx context.Context, organizationID, addonID, changedBy string) error {
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectTypeOrganization, organizationID)
	if err != nil {
		return apperr.Internal(addonDetachFailedCode, addonDetachFailedMsg, err)
	}
	if err := s.repo.lockSubscriptionForUpdate(ctx, s.querier(ctx), sub.ID); err != nil {
		return apperr.Internal(addonDetachFailedCode, addonDetachFailedMsg, err)
	}
	existing, err := s.repo.findAttachedAddon(ctx, s.querier(ctx), sub.ID, addonID)
	if err != nil {
		return apperr.NotFound("ADDON_NOT_FOUND", "addon not found", ErrAddonNotFound)
	}

	if sub.Status == statusTrialing {
		if err := s.repo.deleteSubscriptionAddon(ctx, s.querier(ctx), sub.ID, addonID); err != nil {
			return apperr.Internal(addonDetachFailedCode, addonDetachFailedMsg, err)
		}
		if err := s.insertAddonChangeHistory(ctx, s.querier(ctx), sub, addonID, existing.Quantity, 0, changedBy, nil, nil, false); err != nil {
			return apperr.Internal(addonDetachFailedCode, addonDetachFailedMsg, err)
		}
		return nil
	}
	if sub.ScheduledCancelAt != nil {
		return apperr.Validation(cancellationScheduledCode, cancellationScheduledMsg)
	}
	if err := s.repo.scheduleAddonQuantityChange(ctx, s.querier(ctx), sub.ID, addonID, 0); err != nil {
		return apperr.Internal(addonDetachFailedCode, addonDetachFailedMsg, err)
	}
	phase := historyPhaseScheduled
	if err := s.insertAddonChangeHistory(ctx, s.querier(ctx), sub, addonID, existing.Quantity, 0, changedBy, &phase, sub.PeriodEnd, false); err != nil {
		return apperr.Internal(addonDetachFailedCode, addonDetachFailedMsg, err)
	}
	return nil
}

// undoScheduledAddonQuantityChange clears a scheduled addon quantity change
// (a scheduled decrease or removal) before it ever takes effect. The live
// quantity is untouched — it was never applied. Writes a phase='undone'
// history row mirroring the original 'scheduled' row's from/to quantity —
// what was undone, not the addon's current live quantity.
func (s *service) undoScheduledAddonQuantityChange(
	ctx context.Context, organizationID, addonID, changedBy string,
) (*attachedAddonRecord, error) {
	sub, err := s.getSubscription(ctx, subjectTypeOrganization, organizationID)
	if err != nil {
		return nil, err
	}
	if err := s.repo.lockSubscriptionForUpdate(ctx, s.querier(ctx), sub.ID); err != nil {
		return nil, apperr.Internal("ADDON_UNDO_FAILED", "failed to undo scheduled addon change", err)
	}
	addon, err := s.repo.findAttachedAddon(ctx, s.querier(ctx), sub.ID, addonID)
	if err != nil {
		return nil, apperr.NotFound("ADDON_NOT_FOUND", "addon not found", ErrAddonNotFound)
	}
	if addon.ScheduledQuantity == nil {
		return nil, apperr.Validation("NO_SCHEDULED_ADDON_CHANGE", "no scheduled addon quantity change to undo")
	}
	if err := s.repo.clearScheduledAddonQuantityChange(ctx, s.querier(ctx), sub.ID, addonID); err != nil {
		return nil, apperr.Internal("ADDON_UNDO_FAILED", "failed to undo scheduled addon change", err)
	}
	phase := historyPhaseUndone
	now := time.Now()
	if err := s.insertAddonChangeHistory(ctx, s.querier(ctx), sub, addonID,
		addon.Quantity, *addon.ScheduledQuantity, changedBy, &phase, &now, false); err != nil {
		return nil, apperr.Internal("ADDON_UNDO_FAILED", "failed to undo scheduled addon change", err)
	}
	updated, err := s.repo.findAttachedAddon(ctx, s.querier(ctx), sub.ID, addonID)
	if err != nil {
		return nil, apperr.Internal("ADDON_UNDO_FAILED", "failed to undo scheduled addon change", err)
	}
	return scopeAddonPrice(updated, sub.Currency), nil
}

func (s *service) listAddons(ctx context.Context, organizationID string) ([]attachedAddonRecord, error) {
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectTypeOrganization, organizationID)
	if err != nil {
		return nil, apperr.Internal("ADDONS_FETCH_FAILED", "failed to list addons", err)
	}
	addons, err := s.repo.listAttachedAddonsWithPricing(ctx, s.querier(ctx), sub.ID)
	if err != nil {
		return nil, apperr.Internal("ADDONS_FETCH_FAILED", "failed to list addons", err)
	}
	for i := range addons {
		scopeAddonPrice(&addons[i], sub.Currency)
	}
	return addons, nil
}

// listAddonsCatalog is the existing-subscription counterpart to
// reference.listAddons (unattached catalog options, not what's already on
// the subscription) — see listPlansCatalog (service_subscription.go) for the
// same rationale applied to plans.
func (s *service) listAddonsCatalog(ctx context.Context, organizationID string) ([]contracts.AddonInfo, error) {
	sub, err := s.getSubscription(ctx, subjectTypeOrganization, organizationID)
	if err != nil {
		return nil, err
	}
	addons, err := s.addonsCatalog(ctx)
	if err != nil {
		return nil, apperr.Internal("ADDONS_CATALOG_FETCH_FAILED", "failed to list addons catalog", err)
	}
	for i := range addons {
		addons[i].Prices = scopeCatalogPrices(addons[i].Prices, sub.Currency)
	}
	return addons, nil
}
