package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/db"
)

// computeCouponDiscount resolves a coupon's discount in cents against
// subtotal, clamped so the discount never exceeds the subtotal itself
// (e.g. a $50 fixed coupon on a $9 invoice discounts $9, not $50).
func computeCouponDiscount(discountType string, amountCents *int64, percentOff *int16, subtotal int64) int64 {
	var discount int64
	switch discountType {
	case discountTypeFixed:
		if amountCents != nil {
			discount = *amountCents
		}
	case discountTypePercent:
		if percentOff != nil {
			discount = subtotal * int64(*percentOff) / 100
		}
	}
	if discount > subtotal {
		discount = subtotal
	}
	if discount < 0 {
		discount = 0
	}
	return discount
}

func (s *service) listInvoices(ctx context.Context, subjectType, subjectID string) ([]invoiceRecord, error) {
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, apperr.Internal("INVOICES_FETCH_FAILED", "failed to list invoices", err)
	}
	invoices, err := s.repo.listInvoices(ctx, s.querier(ctx), sub.ID)
	if err != nil {
		return nil, apperr.Internal("INVOICES_FETCH_FAILED", "failed to list invoices", err)
	}
	return invoices, nil
}

func (s *service) listPaymentLinks(ctx context.Context, subjectType, subjectID string) ([]paymentLinkRecord, error) {
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, apperr.Internal("PAYMENT_LINKS_FETCH_FAILED", "failed to list payment links", err)
	}
	links, err := s.repo.listActivePaymentLinks(ctx, s.querier(ctx), sub.ID)
	if err != nil {
		return nil, apperr.Internal("PAYMENT_LINKS_FETCH_FAILED", "failed to list payment links", err)
	}
	return links, nil
}

// regeneratePaymentLink deliberately does not distinguish ErrInvoiceNotPayable
// from any other failure (unlike createPaymentLink's own handler) — matches
// the pre-migration handler, which only special-cased ErrNoRows here.
func (s *service) regeneratePaymentLink(
	ctx context.Context, subjectType, subjectID, invoiceID string,
) (link *paymentLinkRecord, err error) {
	defer func() {
		if err == nil {
			return
		}
		link = nil
		if errors.Is(err, pgx.ErrNoRows) {
			err = apperr.NotFound("INVOICE_NOT_FOUND", "invoice not found", err)
			return
		}
		err = apperr.Internal("PAYMENT_LINK_REGENERATE_FAILED", "failed to regenerate payment link", err)
	}()

	if err := s.repo.expirePendingPaymentLinks(ctx, s.querier(ctx), invoiceID); err != nil {
		return nil, fmt.Errorf("billing.regeneratePaymentLink: %w", err)
	}
	return s.createPaymentLink(ctx, subjectType, subjectID, invoiceID)
}

func (s *service) listPayments(ctx context.Context, subjectType, subjectID string) ([]paymentRecord, error) {
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, apperr.Internal("PAYMENTS_FETCH_FAILED", "failed to list payments", err)
	}
	payments, err := s.repo.listPayments(ctx, s.querier(ctx), sub.ID)
	if err != nil {
		return nil, apperr.Internal("PAYMENTS_FETCH_FAILED", "failed to list payments", err)
	}
	return payments, nil
}

// getInvoicePDFData maps any failure to 404 — matches the pre-migration
// handler, which didn't distinguish ErrNoRows from other errors here.
func (s *service) getInvoicePDFData(
	ctx context.Context, subjectType, subjectID, invoiceID string,
) (inv *invoiceRecord, sub *subscriptionRecord, planInfo *contracts.PlanInfo, lineItems []lineItemRecord, err error) {
	defer func() {
		if err != nil {
			inv, sub, planInfo, lineItems = nil, nil, nil, nil
			err = apperr.NotFound("INVOICE_NOT_FOUND", "invoice not found", err)
		}
	}()

	inv, err = s.repo.findInvoiceByIDAndSubject(ctx, s.querier(ctx), invoiceID, subjectType, subjectID)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("billing.getInvoicePDFData: find invoice: %w", err)
	}
	sub, err = s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("billing.getInvoicePDFData: find subscription: %w", err)
	}
	planInfo, err = s.planCatalog(ctx, sub.Plan)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("billing.getInvoicePDFData: plan catalog: %w", err)
	}
	lineItems, _ = s.repo.listLineItems(ctx, s.querier(ctx), inv.ID)
	return inv, sub, planInfo, lineItems, nil
}

// createPaymentLink creates a Stripe or Xendit payment link for the invoice.
// When subjectType is empty, skips ownership check (internal calls like worker auto-invoice).
// Expires any existing pending links for this invoice before creating a new one (idempotency).
func (s *service) createPaymentLink(
	ctx context.Context, subjectType, subjectID, invoiceID string,
) (*paymentLinkRecord, error) {
	var inv *invoiceRecord
	var err error
	if subjectType != "" {
		inv, err = s.repo.findInvoiceByIDAndSubject(ctx, s.querier(ctx), invoiceID, subjectType, subjectID)
	} else {
		inv, err = s.repo.findInvoiceByID(ctx, s.querier(ctx), invoiceID)
	}
	if err != nil {
		return nil, err
	}
	if inv.Status != statusPending {
		return nil, ErrInvoiceNotPayable
	}

	if existing, err := s.repo.findActivePaymentLinkByInvoice(ctx, s.querier(ctx), invoiceID); err == nil {
		return existing, nil
	}

	_ = s.repo.expirePendingPaymentLinks(ctx, s.querier(ctx), invoiceID)

	provider := selectProvider(inv.Currency)
	var result *paymentLinkResult
	switch provider {
	case providerXendit:
		result, err = createXenditInvoice(ctx, s.provider, invoiceID, inv.AmountCents)
	default:
		result, err = createStripeCheckoutSession(ctx, s.provider, invoiceID, inv.AmountCents, inv.Currency)
	}
	if err != nil {
		return nil, err
	}

	return s.repo.insertPaymentLink(ctx, s.querier(ctx), invoiceID, provider, inv.Currency,
		inv.AmountCents, &result.ExternalID, &result.URL, result.ExpiresAt)
}

// createPaymentLinkForOwner is the handler-facing entry point for
// POST .../invoices/:id/pay — classifies createPaymentLink's error into the
// HTTP contract. createPaymentLink itself stays unwrapped since it's also
// called fire-and-forget internally (provisionSubscription, extendSubscription,
// activateTrialNow, resumeSubscription) and from the worker's auto-invoice
// path, where the detailed underlying error is what gets logged/retried.
func (s *service) createPaymentLinkForOwner(
	ctx context.Context, subjectType, subjectID, invoiceID string,
) (*paymentLinkRecord, error) {
	link, err := s.createPaymentLink(ctx, subjectType, subjectID, invoiceID)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil, apperr.NotFound("INVOICE_NOT_FOUND", "invoice not found", err)
		case errors.Is(err, ErrInvoiceNotPayable):
			return nil, apperr.Validation("UNPROCESSABLE", err.Error())
		default:
			slog.Error("createPaymentLink failed", "invoice_id", invoiceID, "error", err)
			return nil, apperr.Internal("PAYMENT_LINK_CREATE_FAILED", "failed to create payment link", err)
		}
	}
	return link, nil
}

func (s *service) insertPlanLineItem(ctx context.Context, inv *invoiceRecord, planInfo *contracts.PlanInfo) error {
	subtotal := int64(0)
	if inv.SubtotalCents != nil {
		subtotal = *inv.SubtotalCents
	}
	desc := planInfo.Name + " plan"
	return s.repo.insertLineItem(ctx, s.querier(ctx), inv.ID,
		desc, inv.Currency, 1, subtotal, subtotal, 0)
}

// invoicePreview is a read-only composition of what the next invoice would
// look like — either for the subscription's current plan/cycle (the
// pending-changes bar, comparing this against the last real invoice) or a
// hypothetical plan/cycle (the change-plan dialog's proration preview).
// Nothing is written; it reuses composeInvoiceAmount/prorate exactly as
// changePlan does, so this can never drift from what changePlan would
// actually charge.
type invoicePreview struct {
	Plan          string         `json:"plan"`
	Cycle         string         `json:"cycle"`
	Currency      string         `json:"currency"`
	PlanLineCents int64          `json:"plan_line_cents"`
	AddonLines    []lineItemSpec `json:"addon_lines,omitempty"`
	CouponCode    string         `json:"coupon_code,omitempty"`
	DiscountCents int64          `json:"discount_cents,omitempty"`
	TotalCents    int64          `json:"total_cents"`
	// NewPeriodEnd is only set when plan/cycle differ from the subscription's
	// current plan/cycle — a hypothetical-change preview, not the steady-state one.
	NewPeriodEnd *time.Time `json:"new_period_end,omitempty"`
	// Overage is only populated when the hypothetical plan is a downgrade and
	// the organization's current usage exceeds the new plan's limits.
	Overage *overagePreview `json:"overage,omitempty"`
}

type metricOverage struct {
	Current            int      `json:"current"`
	Allowed            int      `json:"allowed"`
	AutoSelectRemovals []string `json:"auto_select_removals"`
}

type overagePreview struct {
	Members metricOverage `json:"members"`
}

func (s *service) previewInvoice(
	ctx context.Context, subjectType, subjectID, plan, cycle string,
) (preview *invoicePreview, err error) {
	defer func() {
		if err == nil {
			return
		}
		preview = nil
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			err = apperr.NotFound("SUBSCRIPTION_NOT_FOUND", "subscription not found", err)
		case errors.Is(err, ErrUnknownPlan):
			err = apperr.Validation("UNKNOWN_PLAN", "unknown plan")
		default:
			err = apperr.Internal("PREVIEW_FAILED", "failed to preview invoice", err)
		}
	}()

	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
	if err != nil {
		return nil, fmt.Errorf("billing.previewInvoice: %w", err)
	}
	targetPlan, targetCycle := plan, cycle
	if targetPlan == "" {
		targetPlan = sub.Plan
	}
	if targetCycle == "" {
		targetCycle = sub.Cycle
	}
	planInfo, err := s.planCatalog(ctx, targetPlan)
	if err != nil {
		return nil, ErrUnknownPlan
	}

	total, addonLines, couponCode, discountCents := s.composeInvoiceAmount(
		ctx, s.querier(ctx), sub.ID, planInfo, sub.Currency, targetCycle)

	preview = &invoicePreview{
		Plan: targetPlan, Cycle: targetCycle, Currency: sub.Currency,
		PlanLineCents: planInfo.Price(sub.Currency, targetCycle),
		AddonLines:    addonLines, CouponCode: couponCode, DiscountCents: discountCents,
		TotalCents: total,
	}

	changingPlan := targetPlan != sub.Plan || targetCycle != sub.Cycle
	if changingPlan {
		if oldPlanInfo, err := s.planCatalog(ctx, sub.Plan); err == nil {
			if sub.PeriodStart != nil && sub.PeriodEnd != nil && sub.Status != statusTrialing {
				oldPrice := int(oldPlanInfo.Price(sub.Currency, sub.Cycle))
				newPrice := int(planInfo.Price(sub.Currency, targetCycle))
				preview.NewPeriodEnd = new(prorate(time.Now(), *sub.PeriodStart,
					*sub.PeriodEnd, oldPrice, newPrice, sub.Cycle, targetCycle))
			}

			// Dry-run overage resolution if this is a downgrade
			if planInfo.SortOrder < oldPlanInfo.SortOrder && s.orgCommander != nil {
				memberLimit := s.downgradeTargetLimits(ctx, sub, planInfo)
				res, err := s.orgCommander.ResolveDowngradeOverage(ctx, subjectID,
					nil, memberLimit, true)
				if err == nil {
					usages, _ := s.repo.listCurrentUsage(ctx, s.querier(ctx), subjectID)
					var curMembers int
					for _, u := range usages {
						if u.Metric == "members" {
							curMembers = int(u.Value)
						}
					}
					preview.Overage = &overagePreview{
						Members: metricOverage{
							Current:            curMembers,
							Allowed:            memberLimit,
							AutoSelectRemovals: res.AutoSelectedMemberSubs,
						},
					}
				} else {
					slog.Error("previewInvoice: failed to dry-run overage resolution",
						"organization_id", subjectID, "error", err)
				}
			}
		}
	} else if s.orgCommander != nil {
		// Not a hypothetical change — preview whatever is already scheduled
		// to apply at renewal (a plan downgrade and/or addon decreases),
		// using the same combined future-state computation the renewal
		// worker itself applies from, so this preview can never disagree
		// with what actually happens at renewal.
		scheduledAddons, err := s.repo.listScheduledAddonChanges(ctx, s.querier(ctx), sub.ID)
		if err == nil && (sub.ScheduledPlan != nil || len(scheduledAddons) > 0) {
			overage, err := s.resolveFutureOveragePreview(ctx, sub)
			if err != nil {
				slog.Error("previewInvoice: failed to compute future overage preview",
					"organization_id", subjectID, "error", err)
			} else if overage.MemberLimit >= 0 && overage.CurrentMembers > int64(overage.MemberLimit) {
				res, err := s.orgCommander.ResolveDowngradeOverage(ctx, subjectID,
					nil, overage.MemberLimit, true)
				if err == nil {
					preview.Overage = &overagePreview{
						Members: metricOverage{
							Current: int(overage.CurrentMembers), Allowed: overage.MemberLimit,
							AutoSelectRemovals: res.AutoSelectedMemberSubs,
						},
					}
				} else {
					slog.Error("previewInvoice: failed to dry-run scheduled overage resolution",
						"organization_id", subjectID, "error", err)
				}
			}
		}
	}

	return preview, nil
}

// lineItemSpec is a single additive charge composeInvoiceAmount resolved
// (currently only ever an attached addon), inserted as an invoice line item
// once the invoice row itself exists.
type lineItemSpec struct {
	Description    string `json:"description"`
	Quantity       int    `json:"quantity"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	TotalCents     int64  `json:"total_cents"`
}

// composeInvoiceAmount resolves a subscription's full invoice subtotal:
// plan price + attached addon charges - any live coupon discount, clamped
// to >= 0. Centralizes what every invoice-creation call site
// (provisionSubscription, changePlan, resumeSubscription,
// HandleSubscriptionAutoInvoice) computes.
func (s *service) composeInvoiceAmount(
	ctx context.Context, q db.Querier, subscriptionID string,
	planInfo *contracts.PlanInfo, currency, cycle string,
) (subtotal int64, addonLines []lineItemSpec, couponCode string, discountCents int64) {
	subtotal = planInfo.Price(currency, cycle)

	if addons, err := s.repo.listAttachedAddonsWithPricing(ctx, q, subscriptionID); err == nil {
		for _, a := range addons {
			unitPrice := int64(0)
			if prices, ok := a.Prices[currency]; ok {
				unitPrice = int64(prices.Monthly)
				if cycle == cycleYearly {
					unitPrice = int64(prices.Yearly)
				}
			}
			total := unitPrice * int64(a.Quantity)
			subtotal += total
			addonLines = append(addonLines, lineItemSpec{
				Description: a.Name, Quantity: a.Quantity,
				UnitPriceCents: unitPrice, TotalCents: total,
			})
		}
	}

	if redemption, err := s.findActiveCouponRedemption(ctx, q, subscriptionID); err == nil {
		discountCents = computeCouponDiscount(redemption.DiscountType,
			redemption.AmountCents, redemption.PercentOff, subtotal)
		if discountCents > 0 {
			subtotal -= discountCents
			couponCode = redemption.CouponCode
		}
	}

	return subtotal, addonLines, couponCode, discountCents
}

// applyInvoiceCharges inserts the addon/discount line items
// composeInvoiceAmount resolved and records the coupon's applied_count
// bump. Call once per invoice, after insertInvoice + insertPlanLineItem
// have already run (line items need a real invoice_id).
func (s *service) applyInvoiceCharges(
	ctx context.Context, q db.Querier, subscriptionID, invoiceID, currency string,
	addonLines []lineItemSpec, couponCode string, discountCents int64,
) error {
	sortOrder := 1
	for _, line := range addonLines {
		if err := s.repo.insertLineItem(ctx, q, invoiceID, line.Description,
			currency, line.Quantity, line.UnitPriceCents, line.TotalCents, sortOrder); err != nil {
			return err
		}
		sortOrder++
	}
	if couponCode != "" {
		if err := s.repo.insertLineItem(ctx, q, invoiceID, "Discount: "+couponCode,
			currency, 1, -discountCents, -discountCents, sortOrder); err != nil {
			return err
		}
		if err := s.repo.incrementCouponRedemptionApplied(ctx, q, subscriptionID, couponCode); err != nil {
			return err
		}
	}
	return nil
}

func selectProvider(currency string) string {
	if currency == currencyIDR {
		return providerXendit
	}
	return providerStripe
}
