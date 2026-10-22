package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/db"
)

const (
	statusActive    = "active"
	statusTrialing  = "trialing"
	statusCancelled = "cancelled"
	statusExpired   = "expired"
	statusFailed    = "failed"
	statusPending   = "pending"
	statusPaid      = "paid"
	statusPastDue   = "past_due"
	currencyIDR     = "IDR"
	currencyUSD     = "USD"
	providerStripe  = "stripe"
	providerXendit  = "xendit"

	cycleMonthly = "monthly"
	cycleYearly  = "yearly"

	// subjectTypeOrganization is the only subjectType this codebase's
	// subject-polymorphic subscription model currently exercises — the
	// field exists for a future non-organization subject, not used yet.
	subjectTypeOrganization = "organization"

	cadenceForever  = "forever"
	cadenceOnce     = "once"
	cadenceRepeated = "repeated"

	discountTypeFixed   = "fixed"
	discountTypePercent = "percent"

	// changedBy sentinels — history.changed_by is either one of these two
	// literal markers (non-owner-initiated transitions) or a real auth_sub.
	changedBySystem  = "system"
	changedByWebhook = "webhook"

	// actorKind values for historyView.ChangedByKind (resolveChangedBy).
	actorKindUser    = "user"
	actorKindSystem  = "system"
	actorKindWebhook = "webhook"

	displayNameSystem  = "System"
	displayNameWebhook = "Webhook"

	// subscription_history.phase — a scheduled amendment's first history
	// row gets historyPhaseScheduled; the renewal worker writes a second row
	// with historyPhaseApplied when it actually applies the amendment.
	historyPhaseScheduled = "scheduled"
	historyPhaseApplied   = "applied"
	historyPhaseUndone    = "undone"

	// actionDowngrade is subscription_history.action for a plan downgrade,
	// shared by the immediate-trial path, the schedule-at-renewal path, and
	// the renewal worker's own apply step.
	actionDowngrade = "downgrade"

	// Shared apperr code/message for ErrCancellationScheduled, classified
	// identically everywhere it's returned (plan downgrade and addon
	// scheduling both reject the same way against the same condition).
	cancellationScheduledCode = "CANCELLATION_SCHEDULED"
	cancellationScheduledMsg  = "subscription is scheduled to cancel; undo that first"
)

var (
	ErrInvoiceNotPayable           = errors.New("invoice is not in a payable state")
	ErrSubscriptionNotCancellable  = errors.New("subscription is not in a cancellable state")
	ErrFeatureNotAvailable         = errors.New("feature not available on current plan")
	ErrCouponNotFound              = errors.New("coupon not found")
	ErrCouponNotRedeemable         = errors.New("coupon not redeemable")
	ErrAddonNotFound               = errors.New("addon not found")
	ErrUnknownPlan                 = errors.New("unknown plan")
	ErrSubscriptionNotExtendable   = errors.New("subscription is not in an extendable state")
	ErrExtensionAlreadyPending     = errors.New("an invoice is already pending payment for this subscription")
	ErrExtensionExceedsMaxDuration = errors.New("extension would exceed the maximum subscription duration")
	ErrAlreadyYearly               = errors.New("subscription is already on the yearly cycle")
	ErrSubscriptionNotTrialing     = errors.New("subscription is not currently trialing")
	ErrSubscriptionNotResumable    = errors.New("subscription is not in a resumable state")
	ErrPlanChangeNotAllowed        = errors.New("subscription is not in a state that allows plan changes")
	ErrNoScheduledCancellation     = errors.New("no scheduled cancellation to undo")
)

func calculateTax(subtotalCents int64, taxRateBPS int) int64 {
	return subtotalCents * int64(taxRateBPS) / 10000
}

func countryFromCurrency(currency string) string {
	if currency == currencyIDR {
		return "ID"
	}
	return "US"
}

type service struct {
	repo         *repository
	pool         *pgxpool.Pool
	provider     ProviderConfig
	userReader   contracts.UserReader
	orgReader    contracts.OrganizationReader
	orgCommander contracts.OrganizationCommander
	taxReader    contracts.CountryTaxReader
	orgSuspender contracts.OrganizationSuspender
	// webhookPool is used by exactly one call site, processWebhook's
	// db.WithTx — see module.go's New for why.
	webhookPool *pgxpool.Pool
}

// findPlanByID/listPlansRecords/listFeaturesRecords/listAddonsRecords/findAddonByID
// are the repository reads backing both Module's exported CatalogReader
// methods (module.go, consumed by other modules) and billing's own internal
// plan/feature/addon lookups (planCatalog/addonCatalog/featureCatalog below)
// — billing reads its own schema directly rather than through the interface
// it exports for everyone else.
func (s *service) findPlanByID(ctx context.Context, id string) (*planRecord, error) {
	return s.repo.findPlanByID(ctx, s.querier(ctx), id)
}

func (s *service) listPlansRecords(ctx context.Context) ([]planRecord, error) {
	return s.repo.listPlans(ctx, s.querier(ctx))
}

func (s *service) listFeaturesRecords(ctx context.Context) ([]featureRecord, error) {
	return s.repo.listFeatures(ctx, s.querier(ctx))
}

func (s *service) listAddonsRecords(ctx context.Context) ([]addonRecord, error) {
	return s.repo.listAddons(ctx, s.querier(ctx))
}

func (s *service) findAddonByID(ctx context.Context, id string) (*addonRecord, error) {
	return s.repo.findAddonByID(ctx, s.querier(ctx), id)
}

// planCatalog/plansCatalog/addonCatalog/addonsCatalog/featureCatalog convert
// repository records to contracts types. They back both billing's own
// business logic (subscription lifecycle, entitlements, invoicing) and
// Module's exported CatalogReader methods (module.go) — one path, no
// duplicate record->contracts conversion.
func (s *service) planCatalog(ctx context.Context, id string) (*contracts.PlanInfo, error) {
	p, err := s.findPlanByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("billing.planCatalog: %w", err)
	}
	return planToInfo(p), nil
}

func (s *service) plansCatalog(ctx context.Context) ([]contracts.PlanInfo, error) {
	plans, err := s.listPlansRecords(ctx)
	if err != nil {
		return nil, fmt.Errorf("billing.plansCatalog: %w", err)
	}
	out := make([]contracts.PlanInfo, len(plans))
	for i := range plans {
		out[i] = *planToInfo(&plans[i])
	}
	return out, nil
}

func (s *service) addonCatalog(ctx context.Context, id string) (*contracts.AddonInfo, error) {
	a, err := s.findAddonByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("billing.addonCatalog: %w", err)
	}
	return addonToInfo(a), nil
}

func (s *service) addonsCatalog(ctx context.Context) ([]contracts.AddonInfo, error) {
	addons, err := s.listAddonsRecords(ctx)
	if err != nil {
		return nil, fmt.Errorf("billing.addonsCatalog: %w", err)
	}
	out := make([]contracts.AddonInfo, len(addons))
	for i := range addons {
		out[i] = *addonToInfo(&addons[i])
	}
	return out, nil
}

// scopeCatalogPrices trims a plan/addon's full multi-currency Prices map
// down to a single currency — the org-scoped catalog counterpart of
// scopeAddonPrice (attached addons) and reference.scopedPrices (the
// pre-org-creation, GeoIP-resolved catalog): currency here is always the
// caller's own already-fixed sub.Currency, never resolved from a request.
// Falls back to USD if that currency has no entry, matching
// contracts.ResolveCurrency's own fallback semantics.
func scopeCatalogPrices(prices map[string]contracts.PlanPrices, currency string) map[string]contracts.PlanPrices {
	amount, ok := prices[currency]
	if !ok {
		amount, ok = prices[contracts.CurrencyUSD]
		currency = contracts.CurrencyUSD
	}
	if !ok {
		return map[string]contracts.PlanPrices{}
	}
	return map[string]contracts.PlanPrices{currency: amount}
}

func (s *service) featureCatalog(ctx context.Context) ([]contracts.FeatureInfo, error) {
	features, err := s.listFeaturesRecords(ctx)
	if err != nil {
		return nil, fmt.Errorf("billing.featureCatalog: %w", err)
	}
	out := make([]contracts.FeatureInfo, len(features))
	for i, f := range features {
		info := contracts.FeatureInfo{
			ID: f.ID, Name: f.Name, Description: f.Description, Type: f.Type,
			Active: f.Active, CreatedAt: f.CreatedAt,
		}
		if f.MetricKey != nil {
			info.MetricKey = *f.MetricKey
		}
		out[i] = info
	}
	return out, nil
}

// querier returns the RLS-scoped transaction from context when available
// (injected by the organization RLS middleware), falling back to the shared pool.
func (s *service) querier(ctx context.Context) db.Querier {
	return db.QuerierFromContext(ctx, s.pool)
}

// enqueueEvent writes an outbox row for a billing domain event using
// whatever Querier is already active in ctx (the RLS middleware's in-flight
// transaction when running under it, s.pool otherwise) — so the write
// commits atomically with any other work already inside that same
// transaction, now that Enqueue writes straight to the outbox instead of
// deferring a publish closure until after commit. Callers must propagate a
// non-nil error rather than swallow it — an outbox row that silently fails
// to insert reopens the same fire-and-forget gap Enqueue exists to close.
func (s *service) enqueueEvent(ctx context.Context, routingKey, orgID string, data any) error {
	return events.Enqueue(ctx, s.querier(ctx), events.ExchangeBilling, routingKey, "billing", orgID, data)
}

// cycleDays approximates a billing cycle's length in days for proration
// math. Calendar months vary 28-31 days; proration is inherently
// approximate (see prorate below), so a fixed approximation is used rather
// than exact day-counting between the subscription's actual period dates.
func cycleDays(cycle string) float64 {
	if cycle == cycleYearly {
		return 365
	}
	return 30
}

// prorate adjusts period_end when changing plans mid-cycle.
//
// Both prices are first normalized to a per-day rate using each plan's own
// cycle length, then ratioed — this matters when oldCycle != newCycle
// (e.g. monthly -> yearly): ratioing the raw period prices directly would
// treat a yearly price as if it covered the same span as a monthly price,
// producing a wildly wrong (shrunk) remaining period. Normalizing per-day
// first correctly answers "how many days of the new plan does the unused
// value of the old plan buy."
//
// unused_value  = old_price_per_day * remaining_days
// new_remaining = unused_value / new_price_per_day
//
//	= remaining_days * (old_price_per_day / new_price_per_day)
func prorate(now, periodStart, periodEnd time.Time, oldPriceCents, newPriceCents int, oldCycle, newCycle string) time.Time {
	if newPriceCents <= 0 {
		return now
	}
	total := periodEnd.Sub(periodStart)
	remaining := periodEnd.Sub(now)
	if remaining <= 0 || total <= 0 {
		return now
	}
	oldPricePerDay := float64(oldPriceCents) / cycleDays(oldCycle)
	newPricePerDay := float64(newPriceCents) / cycleDays(newCycle)
	if newPricePerDay <= 0 {
		return now
	}
	ratio := oldPricePerDay / newPricePerDay
	newRemaining := time.Duration(float64(remaining) * ratio)
	return now.Add(newRemaining)
}

// computeAddonIncreaseProration prices an addon quantity increase for the
// fraction of the current billing period remaining — full-price credit for
// a same-day request, shrinking linearly to near-zero for a request made
// the day before renewal. delta is the additional quantity being requested
// (newQty - liveQty), unitPriceCents is the addon's normal full-cycle
// per-unit price. The reference length is the period's own real span
// (periodEnd - periodStart), not a fixed 30/365-day assumption — a fixed
// reference would over-charge in a 31-day month and under-charge in a
// 28-day one, even for a same-day request that should cost exactly
// unitPriceCents * delta. Unlike prorate() (which shifts period_end for a
// plan change), this returns a cash amount for a period that isn't moving —
// the renewal that follows still bills the full new quantity at its normal
// full-cycle price; this invoice only covers the partial period.
func computeAddonIncreaseProration(periodStart, now, periodEnd time.Time, delta int, unitPriceCents int64) int64 {
	remaining := periodEnd.Sub(now)
	if remaining <= 0 {
		return 0
	}
	total := periodEnd.Sub(periodStart)
	if total <= 0 {
		return 0
	}
	return int64(float64(unitPriceCents) * float64(delta) * (remaining.Seconds() / total.Seconds()))
}

func (s *service) withOrgTx(ctx context.Context, subjectType, orgID string, fn func(tx db.Querier) error) error {
	return db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		txCtx := db.WithQuerier(ctx, tx)
		if subjectType != "" {
			if err := db.SetOrgContext(txCtx, tx, orgID); err != nil {
				return err
			}
		}
		return fn(tx)
	})
}
