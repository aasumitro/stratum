package bootstrap

import (
	"github.com/aasumitro/stratum/internal/modules/account"
	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/modules/notification"
	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/modules/reference"
	"github.com/aasumitro/stratum/internal/platform/mailer"
	"github.com/aasumitro/stratum/internal/platform/storage"
)

// WorkerModules holds every domain module the worker binary consumes
// events for. Its cross-wiring is intentionally a smaller subset than
// APIModules' — the worker never serves HTTP, so it never wires
// SetCacheInvalidator or the catalog-reader setters on organization/
// reference (nothing worker-side needs plan/feature/addon data through
// another module). A setter used only by a worker-only code path must
// still be wired here, not just in APIModules — this struct is what the
// worker binary actually constructs, so anything missing here is simply
// nil at runtime for the worker, regardless of what APIModules does.
//
// storageClient (unlike cache invalidation/catalog reading) IS wired —
// organization's HandleOrganizationDeleted worker consumer needs it to
// purge a deleted organization's logo.
//
// The remaining optional setters (billing reader/writer, user reader,
// organization commander) ARE wired here even though no worker consumer
// currently reaches the code paths that use them — every one of those
// paths is nil-guarded and fails open (skips a plan-limit check, omits an
// inviter's name, etc.) rather than panicking, so an unwired setter is
// silent-degradation risk, not a crash risk. Wiring them keeps the worker's
// dependency graph honest for the day some consumer does reach one, instead
// of relying on "nothing needs it yet" staying true forever.
type WorkerModules struct {
	Organization *organization.Module
	Account      *account.Module
	Reference    *reference.Module
	Billing      *billing.Module
	Notification *notification.Module
}

// NewWorkerModules constructs and cross-wires every module the worker
// consumes events for. storageClient may be nil (Storage.URL unconfigured,
// same convention as NewAPIModules) — organization.SetStorageClient is
// nil-safe either way.
func NewWorkerModules(infra *Infra, storageClient *storage.Client) *WorkerModules {
	cfg := infra.Cfg

	refMod := reference.New(infra.Pool)
	organizationMod := organization.New(infra.Pool, infra.MQPublisher)
	accountMod := account.New(infra.Pool, infra.MQPublisher, cfg.Auth.AdminURL,
		cfg.Auth.ServiceRoleKey, nil, nil, "")
	billingMod := billing.New(
		infra.Pool, infra.MQPublisher,
		billing.ProviderConfig{
			StripeAPIKey:        cfg.Stripe.APIKey,
			StripeWebhookSecret: cfg.Stripe.WebhookSecret,
			StripeSuccessURL:    cfg.Stripe.SuccessURL,
			StripeCancelURL:     cfg.Stripe.CancelURL,
			XenditAPIKey:        cfg.Xendit.APIKey,
			XenditCallbackToken: cfg.Xendit.CallbackToken,
			XenditAllowedCIDRs:  cfg.Xendit.AllowedCIDRs,
		},
		refMod, organizationMod,
	)
	mailClient := mailer.New(cfg.SMTP)
	notifMod := notification.New(infra.Pool, mailClient,
		organizationMod, accountMod, cfg.AppURL, infra.Redis)

	accountMod.SetOrganizationWriter(organizationMod)
	accountMod.SetNotificationWriter(notifMod)
	accountMod.SetBillingWriter(billingMod)
	accountMod.SetOrganizationReader(organizationMod)
	accountMod.SetNotificationReader(notifMod)
	// provisionSubscription's trial-eligibility check runs only here (the
	// organization-created consumer) — without this, every organization
	// would silently look like the owner's first (always trial-eligible).
	billingMod.SetOrganizationReader(organizationMod)
	billingMod.SetUserReader(accountMod)
	billingMod.SetOrganizationCommander(organizationMod)
	organizationMod.SetBillingReader(billingMod)
	organizationMod.SetBillingWriter(billingMod)
	organizationMod.SetUserReader(accountMod)
	organizationMod.SetStorageClient(storageClient)

	accountMod.MustBeWired()

	return &WorkerModules{
		Organization: organizationMod,
		Account:      accountMod,
		Reference:    refMod,
		Billing:      billingMod,
		Notification: notifMod,
	}
}
