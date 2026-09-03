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
			// Round to nearest cent, not truncate — truncation would bias
			// every percent-off discount down, systematically overcharging
			// the customer by up to a cent on every invoice.
			discount = (subtotal*int64(*percentOff) + 50) / 100
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
	lineItems, err = s.repo.listLineItems(ctx, s.querier(ctx), inv.ID)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("billing.getInvoicePDFData: list line items: %w", err)
	}
	return inv, sub, planInfo, lineItems, nil
}

// createPaymentLink creates a Stripe or Xendit payment link for the invoice.
// When subjectType is empty, skips ownership check (internal calls like worker auto-invoice);
// also skips SetOrgContext; only safe for callers guaranteed to run under a BYPASSRLS connection (cmd/worker).
// Expires any existing pending links for this invoice before creating a new one (idempotency).
func (s *service) createPaymentLink(
	ctx context.Context, subjectType, subjectID, invoiceID string,
) (*paymentLinkRecord, error) {
	var inv *invoiceRecord
	err := s.withOrgTx(ctx, subjectType, subjectID, func(tx db.Querier) error {
		txCtx := db.WithQuerier(ctx, tx)
		var findErr error
		if subjectType != "" {
			inv, findErr = s.repo.findInvoiceByIDAndSubject(txCtx, tx, invoiceID, subjectType, subjectID)
		} else {
			inv, findErr = s.repo.findInvoiceByID(txCtx, tx, invoiceID)
		}
		return findErr
	})
	if err != nil {
		return nil, err
	}
	if inv.Status != statusPending {
		return nil, ErrInvoiceNotPayable
	}

	var existing *paymentLinkRecord
	findErr := s.withOrgTx(ctx, subjectType, subjectID, func(tx db.Querier) error {
		txCtx := db.WithQuerier(ctx, tx)
		var err error
		existing, err = s.repo.findActivePaymentLinkByInvoice(txCtx, tx, invoiceID)
		return err
	})
	if findErr == nil {
		return existing, nil
	}

	expErr := s.withOrgTx(ctx, subjectType, subjectID, func(tx db.Querier) error {
		txCtx := db.WithQuerier(ctx, tx)
		return s.repo.expirePendingPaymentLinks(txCtx, tx, invoiceID)
	})
	if expErr != nil {
		return nil, fmt.Errorf("billing.createPaymentLink: expire pending payment links: %w", expErr)
	}

	label := invoiceID
	if inv.InvoiceNumber != nil {
		label = *inv.InvoiceNumber
	}
	items := s.checkoutLineItems(ctx, inv)

	provider := selectProvider(inv.Currency)
	var result *paymentLinkResult
	switch provider {
	case providerXendit:
		result, err = createXenditInvoice(ctx, s.provider, invoiceID, label, inv.AmountCents, items)
	default:
		result, err = createStripeCheckoutSession(ctx, s.provider, invoiceID, label, inv.AmountCents, inv.Currency, items)
	}
	if err != nil {
		return nil, err
	}

	var link *paymentLinkRecord
	insErr := s.withOrgTx(ctx, subjectType, subjectID, func(tx db.Querier) error {
		txCtx := db.WithQuerier(ctx, tx)
		var err error
		link, err = s.repo.insertPaymentLink(txCtx, tx, invoiceID, provider, inv.Currency,
			inv.AmountCents, &result.ExternalID, &result.URL, result.ExpiresAt)
		return err
	})
	return link, insErr
}

// checkoutLineItems builds the hosted checkout page's itemized breakdown
// from inv's own stored invoice_line_items (plan/addon charges). A lookup
// failure falls back to nil — the provider functions' own flat-line
// fallback — same as buildCheckoutLineItems' own empty/unsafe cases below.
func (s *service) checkoutLineItems(ctx context.Context, inv *invoiceRecord) []checkoutLineItem {
	lines, err := s.repo.listLineItems(ctx, s.querier(ctx), inv.ID)
	if err != nil {
		return nil
	}
	return buildCheckoutLineItems(lines, inv.TaxCents)
}

// buildCheckoutLineItems is checkoutLineItems' pure decision logic, split
// out so it's unit-testable without a database: given an invoice's stored
// line items plus its tax, decide whether it's safe to itemize the hosted
// checkout page and build that list, or bail to nil (the provider
// functions' own flat "Invoice <label>" fallback). Returns nil whenever
// itemizing would risk the checkout charging something other than the
// invoice's real total: no line items at all, or any line item with a
// non-positive total — the discount line applyInvoiceCharges inserts is
// always negative, and neither Stripe nor Xendit's page can be trusted to
// net a negative line against the rest correctly, so the safe choice is not
// to itemize a discounted invoice at all, not to guess. A positive tax is
// appended as its own trailing line so the itemized sum still equals the
// invoice's real total (subtotal + tax), never just the subtotal.
func buildCheckoutLineItems(lines []lineItemRecord, taxCents int64) []checkoutLineItem {
	if len(lines) == 0 {
		return nil
	}
	items := make([]checkoutLineItem, 0, len(lines)+1)
	for _, l := range lines {
		if l.TotalCents <= 0 {
			return nil
		}
		items = append(items, checkoutLineItem{Name: l.Description, Quantity: l.Quantity, UnitAmountCents: l.UnitPriceCents})
	}
	if taxCents > 0 {
		items = append(items, checkoutLineItem{Name: "Tax", Quantity: 1, UnitAmountCents: taxCents})
	}
	return items
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

	total, addonLines, couponCode, discountCents, err := s.composeInvoiceAmount(
		ctx, s.querier(ctx), sub.ID, planInfo, sub.Currency, targetCycle)
	if err != nil {
		return nil, err
	}

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
				// Mirrors changePlanWithMetadata's own guard (service_subscription.go):
				// only an actual plan-tier change re-derives period_end via
				// price-ratio proration. A pure cycle switch on the same plan
				// doesn't move period_end — see that function's comment for why
				// re-pricing already-banked time at the new cycle's rate would be
				// wrong — so the preview must show the unchanged date, not a
				// prorated one the real change would never produce.
				if targetPlan != sub.Plan {
					oldPrice := int(oldPlanInfo.Price(sub.Currency, sub.Cycle))
					newPrice := int(planInfo.Price(sub.Currency, targetCycle))
					preview.NewPeriodEnd = new(prorate(time.Now(), *sub.PeriodStart,
						*sub.PeriodEnd, oldPrice, newPrice, sub.Cycle, targetCycle))
				} else {
					preview.NewPeriodEnd = sub.PeriodEnd
				}
			}

			// Dry-run overage resolution if this is a downgrade
			if planInfo.SortOrder < oldPlanInfo.SortOrder && s.orgCommander != nil {
				if overage, err := s.hypotheticalDowngradeOveragePreview(ctx, subjectID, sub, planInfo); err == nil {
					preview.Overage = overage
				} else {
					slog.Error("previewInvoice: failed to dry-run overage resolution",
						"organization_id", subjectID, "error", err)
				}
			}
		}
	} else if s.orgCommander != nil {
		// Not a hypothetical change — preview whatever is already scheduled
		// to apply at renewal (a plan downgrade and/or addon decreases).
		if overage, err := s.scheduledOveragePreview(ctx, subjectID, sub); err != nil {
			slog.Error("previewInvoice: failed to compute scheduled overage preview",
				"organization_id", subjectID, "error", err)
		} else if overage != nil {
			preview.Overage = overage
		}
	}

	return preview, nil
}

// hypotheticalDowngradeOveragePreview dry-runs member-overage resolution
// for a hypothetical plan/cycle change that's a downgrade — how many
// members would need to be removed under the new plan's limit, and which
// ones ResolveDowngradeOverage would auto-select. Read-only: dryRun=true on
// the ResolveDowngradeOverage call means nothing is actually removed.
func (s *service) hypotheticalDowngradeOveragePreview(
	ctx context.Context, subjectID string, sub *subscriptionRecord, planInfo *contracts.PlanInfo,
) (*overagePreview, error) {
	memberLimit := s.downgradeTargetLimits(ctx, sub, planInfo)
	res, err := s.orgCommander.ResolveDowngradeOverage(ctx, subjectID, nil, memberLimit, true)
	if err != nil {
		return nil, err
	}
	usages, _ := s.repo.listCurrentUsage(ctx, s.querier(ctx), subjectID)
	var curMembers int
	for _, u := range usages {
		if u.Metric == "members" {
			curMembers = int(u.Value)
		}
	}
	return &overagePreview{
		Members: metricOverage{
			Current:            curMembers,
			Allowed:            memberLimit,
			AutoSelectRemovals: res.AutoSelectedMemberSubs,
		},
	}, nil
}

// scheduledOveragePreview dry-runs member-overage resolution for whatever
// amendment is already scheduled to apply at sub's next renewal (a plan
// downgrade and/or addon decreases), using the same combined future-state
// computation the renewal worker itself applies from
// (resolveFutureOveragePreview) — so this preview can never disagree with
// what actually happens at renewal. Returns (nil, nil), not an error, both
// when nothing is scheduled and when what's scheduled wouldn't put the
// organization over its future limit — both are steady states, not
// failures.
func (s *service) scheduledOveragePreview(ctx context.Context, subjectID string, sub *subscriptionRecord) (*overagePreview, error) {
	scheduledAddons, err := s.repo.listScheduledAddonChanges(ctx, s.querier(ctx), sub.ID)
	if err != nil || (sub.ScheduledPlan == nil && len(scheduledAddons) == 0) {
		return nil, nil
	}
	overage, err := s.resolveFutureOveragePreview(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("compute future overage preview: %w", err)
	}
	if overage.MemberLimit < 0 || overage.CurrentMembers <= int64(overage.MemberLimit) {
		return nil, nil
	}
	res, err := s.orgCommander.ResolveDowngradeOverage(ctx, subjectID, nil, overage.MemberLimit, true)
	if err != nil {
		return nil, fmt.Errorf("dry-run scheduled overage resolution: %w", err)
	}
	return &overagePreview{
		Members: metricOverage{
			Current: int(overage.CurrentMembers), Allowed: overage.MemberLimit,
			AutoSelectRemovals: res.AutoSelectedMemberSubs,
		},
	}, nil
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
// HandleSubscriptionAutoInvoice) computes. A genuine lookup failure on either
// the addon pricing or the coupon redemption is propagated rather than
// swallowed — either one silently succeeding with a wrong subtotal would
// mean the customer is charged the wrong amount, not just shown a
// stale/incomplete display. pgx.ErrNoRows from findActiveCouponRedemption
// (no active coupon) is the expected steady-state case, not an error here.
func (s *service) composeInvoiceAmount(
	ctx context.Context, q db.Querier, subscriptionID string,
	planInfo *contracts.PlanInfo, currency, cycle string,
) (subtotal int64, addonLines []lineItemSpec, couponCode string, discountCents int64, err error) {
	subtotal = planInfo.Price(currency, cycle)

	addons, err := s.repo.listAttachedAddonsWithPricing(ctx, q, subscriptionID)
	if err != nil {
		return 0, nil, "", 0, fmt.Errorf("billing.composeInvoiceAmount: list attached addons: %w", err)
	}
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

	redemption, err := s.findActiveCouponRedemption(ctx, q, subscriptionID)
	switch {
	case err == nil:
		discountCents = computeCouponDiscount(redemption.DiscountType,
			redemption.AmountCents, redemption.PercentOff, subtotal)
		if discountCents > 0 {
			subtotal -= discountCents
			couponCode = redemption.CouponCode
		}
	case errors.Is(err, pgx.ErrNoRows):
		// No active coupon — expected steady state, nothing to apply.
	default:
		return 0, nil, "", 0,
			fmt.Errorf("billing.composeInvoiceAmount: find active coupon redemption: %w", err)
	}

	return subtotal, addonLines, couponCode, discountCents, nil
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
