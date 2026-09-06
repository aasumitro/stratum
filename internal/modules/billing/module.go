package billing

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
)

// Module owns subscriptions, invoices, payment links, and the plan/feature/
// addon catalog. Implements contracts.BillingReader for feature-gating and
// contracts.CatalogReader for catalog lookups by other modules.
type Module struct {
	svc    *service
	Worker *Worker
	cfg    ProviderConfig
}

// New webhookPool is a BYPASSRLS pool used by exactly one call site
// (service.processWebhook) to apply a webhook-driven payment confirmation,
// which arrives with no authenticated org context to satisfy RLS. nil on
// cmd/worker's instance — the worker binary never serves webhooks, so
// s.webhookPool is simply never referenced there.
func New(
	pool *pgxpool.Pool,
	cfg ProviderConfig, taxReader contracts.CountryTaxReader,
	orgSuspender contracts.OrganizationSuspender,
	webhookPool *pgxpool.Pool,
) *Module {
	svc := &service{
		repo: &repository{}, pool: pool, provider: cfg,
		taxReader: taxReader, orgSuspender: orgSuspender, webhookPool: webhookPool,
	}
	return &Module{svc: svc, Worker: &Worker{svc: svc}, cfg: cfg}
}

// SetUserReader wires actor resolution for the subscription history tab —
// resolves a history row's raw changed_by auth_sub to a display name.
// Optional: unwired means history rows fall back to the raw changed_by
// string, same nil-safe convention as this project's other cross-module
// optional dependencies.
func (m *Module) SetUserReader(r contracts.UserReader) {
	m.svc.userReader = r
}

// SetOrganizationReader wires the organization reader used by
// provisionSubscription's trial-eligibility check. Optional: unwired means
// every organization owner is treated as first-time (always gets a trial),
// same fail-open convention as this project's other optional dependencies.
func (m *Module) SetOrganizationReader(r contracts.OrganizationReader) {
	m.svc.orgReader = r
}

// SetOrganizationCommander wires the organization commander used by
// downgradeSubscription's overage resolution. Optional.
func (m *Module) SetOrganizationCommander(c contracts.OrganizationCommander) {
	m.svc.orgCommander = c
}

// GetPlanByID implements contracts.CatalogReader.
func (m *Module) GetPlanByID(ctx context.Context, id string) (*contracts.PlanInfo, error) {
	return m.svc.planCatalog(ctx, id)
}

// ListPlans implements contracts.CatalogReader.
func (m *Module) ListPlans(ctx context.Context) ([]contracts.PlanInfo, error) {
	return m.svc.plansCatalog(ctx)
}

// ListFeatures implements contracts.CatalogReader.
func (m *Module) ListFeatures(ctx context.Context) ([]contracts.FeatureInfo, error) {
	return m.svc.featureCatalog(ctx)
}

// ListAddons implements contracts.CatalogReader.
func (m *Module) ListAddons(ctx context.Context) ([]contracts.AddonInfo, error) {
	return m.svc.addonsCatalog(ctx)
}

// GetAddonByID implements contracts.CatalogReader.
func (m *Module) GetAddonByID(ctx context.Context, id string) (*contracts.AddonInfo, error) {
	return m.svc.addonCatalog(ctx, id)
}

// ValidateCouponCode implements contracts.CatalogReader. Used by the
// organization module to validate a coupon code before an organization (and
// its subscription) exist — see service_coupon.go's validateCouponForSubject.
func (m *Module) ValidateCouponCode(ctx context.Context, code, authSub string) error {
	return m.svc.validateCouponForSubject(ctx, code, authSub)
}

func planToInfo(p *planRecord) *contracts.PlanInfo {
	return &contracts.PlanInfo{
		ID:           p.ID,
		Name:         p.Name,
		Description:  p.Description,
		Prices:       p.Prices,
		Limits:       p.Limits,
		Features:     p.Features,
		SortOrder:    p.SortOrder,
		ConfigValues: p.ConfigValues,
		Active:       p.Active,
		CreatedAt:    p.CreatedAt,
		UpdatedAt:    p.UpdatedAt,
	}
}

func addonToInfo(a *addonRecord) *contracts.AddonInfo {
	return &contracts.AddonInfo{
		ID: a.ID, Name: a.Name, Description: a.Description,
		Prices: a.Prices, Features: a.Features,
		Active: a.Active, CreatedAt: a.CreatedAt,
	}
}

// GetSubscriptionBySubject implements contracts.BillingReader.
func (m *Module) GetSubscriptionBySubject(
	ctx context.Context, subjectType, subjectID string,
) (*contracts.SubscriptionInfo, error) {
	s, err := m.svc.getSubscription(ctx, subjectType, subjectID)
	if err != nil {
		return nil, fmt.Errorf("billing.GetSubscriptionBySubject: %w", err)
	}
	return &contracts.SubscriptionInfo{
		ID:          s.ID,
		SubjectType: s.SubjectType,
		SubjectID:   s.SubjectID,
		Plan:        s.Plan,
		Status:      s.Status,
		Cycle:       s.Cycle,
		Currency:    s.Currency,
	}, nil
}

// CheckUsageLimit implements contracts.BillingReader.
func (m *Module) CheckUsageLimit(ctx context.Context, organizationID, metric string) (int64, int, error) {
	current, limit, err := m.svc.checkUsageLimit(ctx, organizationID, metric)
	if err != nil {
		return current, limit, fmt.Errorf("billing.CheckUsageLimit: %w", err)
	}
	return current, limit, nil
}

// CheckFeatureAccess implements contracts.BillingReader.
func (m *Module) CheckFeatureAccess(ctx context.Context, organizationID, feature string) error {
	return m.svc.checkFeatureAccess(ctx, organizationID, feature)
}

// RecordUsage implements contracts.BillingWriter.
func (m *Module) RecordUsage(ctx context.Context, organizationID, metric string, value int64) error {
	return m.svc.recordUsage(ctx, organizationID, metric, value)
}

// AnonymizeHistory implements contracts.BillingWriter. Called by the account
// module's GDPR delete-account flow.
func (m *Module) AnonymizeHistory(ctx context.Context, authSub string) error {
	return m.svc.anonymizeHistory(ctx, authSub)
}

// Register mounts billing routes under /organizations/:organizationID/billing.
//
//	GET    /organizations/:organizationID/billing
//	PATCH  /organizations/:organizationID/billing/plan
//	POST   /organizations/:organizationID/billing/downgrade
//	POST   /organizations/:organizationID/billing/downgrade/undo
//	POST   /organizations/:organizationID/billing/cancel
//	POST   /organizations/:organizationID/billing/cancel/undo
//	POST   /organizations/:organizationID/billing/resume
//	POST   /organizations/:organizationID/billing/extend
//	POST   /organizations/:organizationID/billing/activate
//	GET    /organizations/:organizationID/billing/history
//	GET    /organizations/:organizationID/billing/invoices
//	POST   /organizations/:organizationID/billing/invoices/:invoiceID/pay
//	GET    /organizations/:organizationID/billing/payments
//	GET    /organizations/:organizationID/billing/payment-links
//	GET    /organizations/:organizationID/billing/usage
//	POST   /organizations/:organizationID/billing/usage
//	GET    /organizations/:organizationID/billing/features
//	POST   /organizations/:organizationID/billing/coupons/redeem
//	POST   /organizations/:organizationID/billing/addons
//	DELETE /organizations/:organizationID/billing/addons/:addonID
//	POST   /organizations/:organizationID/billing/addons/:addonID/undo
//	GET    /organizations/:organizationID/billing/addons
//	GET    /organizations/:organizationID/billing/plans/catalog
//	GET    /organizations/:organizationID/billing/addons/catalog
//	GET    /organizations/:organizationID/billing/preview
//	GET    /organizations/:organizationID/billing/coupons
//	GET    /billing/coupons/eligible — same eligibility check, before an organization exists
//
// mfaGate is applied to plan changes, subscription extension, immediate
// trial activation, and addon attach/detach — an aal2-only gate for users
// who have MFA enabled, see contracts.UserReader.IsMFAEnabled.
func (m *Module) Register(r *gin.RouterGroup, deps httpserver.RouteDeps) {
	h := &handler{svc: m.svc}
	ownerOnly := middleware.RequireRole(contracts.RoleOwner)

	r.GET("/billing/coupons/eligible", deps.Auth, deps.RateLimit, h.listEligibleCouponsForNewOrg)

	billing := r.Group("/organizations/:organizationID/billing")
	billing.Use(deps.Auth, deps.Org, deps.RateLimit, deps.RLS)
	{
		// Every billing tab is viewable by any member — only mutations
		// (plan/cancel/resume/extend/activate/pay/addons/coupon redeem/manual
		// usage correction) are owner-only. GETs used to be blanket
		// owner-only, which silently broke the read-only member/admin view
		// for everything except the bare subscription status check.
		billing.GET("", h.getSubscription)
		billing.GET("/history", h.listHistory)
		billing.GET("/invoices", h.listInvoices)
		billing.GET("/payments", h.listPayments)
		billing.GET("/invoices/:invoiceID/pdf", h.invoicePDF)
		billing.GET("/payment-links", h.listPaymentLinks)
		billing.GET("/usage", h.getUsage)
		billing.GET("/features", h.listFeatures)
		billing.GET("/addons", h.listAddons)
		billing.GET("/plans/catalog", h.listPlansCatalog)
		billing.GET("/addons/catalog", h.listAddonsCatalog)
		billing.GET("/preview", h.previewInvoice)
		billing.GET("/coupons", h.listEligibleCoupons)

		billing.PATCH("/plan", ownerOnly, deps.MFA, h.changePlan)
		billing.POST("/downgrade", ownerOnly, deps.MFA, h.downgradeSubscription)
		billing.POST("/downgrade/undo", ownerOnly, deps.MFA, h.undoDowngrade)
		billing.POST("/cancel", ownerOnly, h.cancelSubscription)
		billing.POST("/cancel/undo", ownerOnly, h.undoCancellation)
		billing.POST("/usage", ownerOnly, h.recordUsage)
		billing.POST("/coupons/redeem", ownerOnly, h.redeemCoupon)
		billing.DELETE("/addons/:addonID", ownerOnly, deps.MFA, h.detachAddon)
		billing.POST("/addons/:addonID/undo", ownerOnly, deps.MFA, h.undoAddonQuantityChange)
	}

	// billingPay deliberately excludes deps.RLS. Every route here can make a
	// blocking Stripe/Xendit HTTP call (createPaymentLink, fire-and-forget or
	// direct); the group-level RLS middleware begins a tx before the handler
	// runs and only commits after it returns, so wrapping these routes in it
	// would hold a pooled DB connection open for the HTTP call's duration —
	// enough concurrent requests during provider latency exhausts the pool
	// and takes down the whole API. Isolation instead relies on explicit
	// organization_id/subject-scoped queries in the repository layer, the
	// same model org/account/notification already run on with no RLS at
	// all. Each service method wraps its own DB-only writes in db.WithTx
	// and runs the provider call after that commits (see extendSubscription).
	billingPay := r.Group("/organizations/:organizationID/billing")
	billingPay.Use(deps.Auth, deps.Org, deps.RateLimit)
	{
		billingPay.POST("/resume", ownerOnly, h.resumeSubscription)
		billingPay.POST("/extend", ownerOnly, deps.MFA, h.extendSubscription)
		billingPay.POST("/activate", ownerOnly, deps.MFA, h.activateTrialNow)
		billingPay.POST("/invoices/:invoiceID/pay", ownerOnly, deps.Idempotency, h.createPaymentLink)
		// attachAddon moved here from the RLS group above: a non-trialing
		// increase now creates a gating invoice and makes the same blocking
		// payment-link HTTP call every other route in this group makes —
		// see attachAddon/requestAddonIncrease (service_addon.go).
		billingPay.POST("/addons", ownerOnly, deps.MFA, h.attachAddon)
	}
}

// RegisterWebhooks mounts public webhook endpoints — no auth middleware.
//
//	POST /webhooks/stripe
//	POST /webhooks/xendit
func (m *Module) RegisterWebhooks(r *gin.RouterGroup) error {
	h := &handler{svc: m.svc, stripeSecret: m.cfg.StripeWebhookSecret, xenditToken: m.cfg.XenditCallbackToken}
	r.POST("/stripe", h.handleStripeWebhook)

	ipAllowlist, err := middleware.NewIPAllowlistMiddleware(m.cfg.XenditAllowedCIDRs)
	if err != nil {
		return fmt.Errorf("billing.RegisterWebhooks: %w", err)
	}
	r.POST("/xendit", ipAllowlist, h.handleXenditWebhook)
	return nil
}
