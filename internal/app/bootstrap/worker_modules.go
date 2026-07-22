package bootstrap

import (
	"github.com/aasumitro/stratum/internal/modules/account"
	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/modules/notification"
	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/modules/reference"
	"github.com/aasumitro/stratum/internal/platform/mailer"
)

// WorkerModules holds every domain module the worker binary consumes
// events for. Its cross-wiring is intentionally a smaller subset than
// APIModules' — the worker never serves HTTP, so it never wires
// SetStorageClient, SetCacheInvalidator, or the catalog-reader setters on
// organization/reference (nothing worker-side needs plan/feature/addon data
// through another module). A setter used only by a worker-only code path
// must still be wired here, not just in APIModules — this struct is what
// the worker binary actually constructs, so anything missing here is
// simply nil at runtime for the worker, regardless of what APIModules does.
type WorkerModules struct {
	Organization *organization.Module
	Account      *account.Module
	Reference    *reference.Module
	Billing      *billing.Module
	Notification *notification.Module
}

// NewWorkerModules constructs and cross-wires every module the worker
// consumes events for.
func NewWorkerModules(infra *Infra) *WorkerModules {
	cfg := infra.Cfg

	refMod := reference.New(infra.Pool)
	organizationMod := organization.New(infra.Pool, infra.MQPublisher)
	accountMod := account.New(infra.Pool, infra.MQPublisher, cfg.Auth.AdminURL, cfg.Auth.ServiceRoleKey, nil, nil, "")
	billingMod := billing.New(infra.Pool, infra.MQPublisher, billing.ProviderConfig{
		StripeAPIKey:        cfg.Stripe.APIKey,
		StripeWebhookSecret: cfg.Stripe.WebhookSecret,
		StripeSuccessURL:    cfg.Stripe.SuccessURL,
		StripeCancelURL:     cfg.Stripe.CancelURL,
		XenditAPIKey:        cfg.Xendit.APIKey,
		XenditCallbackToken: cfg.Xendit.CallbackToken,
	}, refMod, organizationMod)
	mailClient := mailer.New(cfg.SMTP)
	notifMod := notification.New(infra.Pool, mailClient, organizationMod, accountMod, cfg.AppURL, infra.Redis)

	accountMod.SetOrganizationWriter(organizationMod)
	accountMod.SetNotificationWriter(notifMod)
	accountMod.SetBillingWriter(billingMod)
	accountMod.SetOrganizationReader(organizationMod)
	accountMod.SetNotificationReader(notifMod)
	// provisionSubscription's trial-eligibility check runs only here (the
	// organization-created consumer) — without this, every organization
	// would silently look like the owner's first (always trial-eligible).
	billingMod.SetOrganizationReader(organizationMod)

	accountMod.MustBeWired()

	return &WorkerModules{
		Organization: organizationMod,
		Account:      accountMod,
		Reference:    refMod,
		Billing:      billingMod,
		Notification: notifMod,
	}
}
