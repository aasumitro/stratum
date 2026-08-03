package billing

import (
	"context"
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
func (s *service) attachAddon(ctx context.Context, organizationID, addonID string, quantity int) (*attachedAddonRecord, error) {
	addonInfo, err := s.addonCatalog(ctx, addonID)
	if err != nil {
		return nil, apperr.NotFound("ADDON_NOT_FOUND", "addon not found", ErrAddonNotFound)
	}
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectTypeOrganization, organizationID)
	if err != nil {
		return nil, apperr.Internal(addonAttachFailedCode, addonAttachFailedMsg, err)
	}

	var pendingInvoice *invoiceRecord
	err = db.WithTx(ctx, s.pool, func(tx db.Querier) error {
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
			if sub.Status == statusTrialing {
				return s.repo.upsertSubscriptionAddon(txCtx, tx, sub.ID, addonID, quantity)
			}
			if quantity == existingQuantity {
				return nil // no-op: nothing changed, nothing to bill
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
			inv, err := s.requestAddonIncrease(txCtx, tx, sub, addonID, addonInfo, existingQuantity, quantity)
			if err != nil {
				return err
			}
			pendingInvoice = inv
			return nil
		case sub.Status == statusTrialing:
			return s.repo.upsertSubscriptionAddon(txCtx, tx, sub.ID, addonID, quantity)
		default:
			if sub.ScheduledCancelAt != nil {
				return apperr.Validation(cancellationScheduledCode, cancellationScheduledMsg)
			}
			return s.repo.scheduleAddonQuantityChange(txCtx, tx, sub.ID, addonID, quantity)
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
		_, _ = s.createPaymentLink(ctx, "", "", pendingInvoice.ID)
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
	return addon, nil
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
	existingQuantity, quantity int,
) (*invoiceRecord, error) {
	delta := quantity - existingQuantity

	unitPrice := int64(0)
	if prices, ok := addonInfo.Prices[sub.Currency]; ok {
		unitPrice = int64(prices.Monthly)
		if sub.Cycle == cycleYearly {
			unitPrice = int64(prices.Yearly)
		}
	}

	preDiscount := computeAddonIncreaseProration(*sub.PeriodStart, time.Now(), *sub.PeriodEnd, delta, unitPrice)

	var couponCode string
	var discountCents int64
	if redemption, err := s.findActiveCouponRedemption(ctx, tx, sub.ID); err == nil {
		discountCents = computeCouponDiscount(redemption.DiscountType, redemption.AmountCents, redemption.PercentOff, preDiscount)
		if discountCents > 0 {
			couponCode = redemption.CouponCode
		}
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
		return nil, nil
	}

	inv, err := s.repo.insertInvoice(ctx, tx, sub.SubjectID, sub.ID, subtotal, taxRate, tax, sub.Currency, "addon_increase", false)
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

	return inv, nil
}

// detachAddon removes an addon. Trialing subscriptions delete the row
// immediately, matching today's behavior. Active subscriptions schedule the
// removal (quantity 0 at renewal) instead, so the live quantity keeps being
// read by checkUsageLimit until the renewal worker applies it.
func (s *service) detachAddon(ctx context.Context, organizationID, addonID string) error {
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectTypeOrganization, organizationID)
	if err != nil {
		return apperr.Internal(addonDetachFailedCode, addonDetachFailedMsg, err)
	}
	if err := s.repo.lockSubscriptionForUpdate(ctx, s.querier(ctx), sub.ID); err != nil {
		return apperr.Internal(addonDetachFailedCode, addonDetachFailedMsg, err)
	}

	if sub.Status == statusTrialing {
		if err := s.repo.deleteSubscriptionAddon(ctx, s.querier(ctx), sub.ID, addonID); err != nil {
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
	return nil
}

// undoScheduledAddonQuantityChange clears a scheduled addon quantity change
// (a scheduled decrease or removal) before it ever takes effect. The live
// quantity is untouched — it was never applied.
func (s *service) undoScheduledAddonQuantityChange(
	ctx context.Context, organizationID, addonID string,
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
	return s.repo.findAttachedAddon(ctx, s.querier(ctx), sub.ID, addonID)
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
	return addons, nil
}
