package bootstrap

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/modules/account"
	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/modules/notification"
	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/modules/reference"
	"github.com/aasumitro/stratum/internal/platform/cache"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/mailer"
	"github.com/aasumitro/stratum/internal/platform/storage"
)

// APIModules holds every domain module the API serves, already
// cross-wired, plus the middleware built directly from those
// modules (auth, organization resolution, RLS, MFA gate) — as opposed to
// generic HTTP middleware (CORS, rate limiting), which belongs to the
// router instead.
type APIModules struct {
	Organization *organization.Module
	Account      *account.Module
	Reference    *reference.Module
	Billing      *billing.Module
	Notification *notification.Module

	AuthMW gin.HandlerFunc
	// AuthSSEMW is identical to AuthMW but additionally accepts the
	// __session cookie as a fallback — wire this only on the SSE stream
	// route, since EventSource can't set the Authorization header.
	AuthSSEMW gin.HandlerFunc
	OrgMW     gin.HandlerFunc
	RLSMW     gin.HandlerFunc
	MFAMW     gin.HandlerFunc
}

// NewAPIModules constructs and cross-wires every module the API serves.
// storageClient may be nil (Storage.URL unconfigured) — organization's
// SetStorageClient is nil-safe, matching every other optional dependency
// in this codebase.
func NewAPIModules(ctx context.Context, infra *Infra, storageClient *storage.Client) (*APIModules, error) {
	cfg := infra.Cfg

	// Redis namespace shared between account service (write revoked tokens)
	// and auth middleware (read revoked tokens).
	accountNS := cache.NewNamespace(infra.Redis, "account")

	organizationMod := organization.New(infra.Pool, infra.MQPublisher)
	accountMod := account.New(infra.Pool, infra.MQPublisher, cfg.Auth.AdminURL, cfg.Auth.ServiceRoleKey, accountNS, storageClient, cfg.Auth.WebhookSecret)
	refMod := reference.New(infra.Pool)
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

	organizationMod.SetBillingReader(billingMod)
	organizationMod.SetBillingWriter(billingMod)
	organizationMod.SetStorageClient(storageClient)
	organizationMod.SetUserReader(accountMod)
	billingMod.SetUserReader(accountMod)
	billingMod.SetOrganizationReader(organizationMod)
	billingMod.SetOrganizationCommander(organizationMod)
	organizationMod.SetCatalogReader(billingMod)
	refMod.SetCatalogReader(billingMod)
	accountMod.SetOrganizationWriter(organizationMod)
	accountMod.SetNotificationWriter(notifMod)
	accountMod.SetBillingWriter(billingMod)
	accountMod.SetOrganizationReader(organizationMod)
	accountMod.SetNotificationReader(notifMod)

	cachedWSReader := cache.NewCachedOrganizationReader(organizationMod, infra.Redis)
	organizationMod.SetCacheInvalidator(cachedWSReader)

	authMW, authSSEMW, err := middleware.NewAuthMiddleware(ctx, cfg.Auth, middleware.AuthHooks{
		OnAuth: func(ctx context.Context, authSub string) {
			_, _ = infra.Pool.Exec(ctx, `UPDATE account.users SET last_seen_at = now() WHERE auth_sub = $1`, authSub)
		},
		IsRevoked: func(ctx context.Context, sessionID string) bool {
			ok, _ := accountNS.Exists(ctx, "revoked_tokens:"+sessionID)
			return ok
		},
		OnLogin: func(ctx context.Context, authSub, ip, ua string) {
			accountMod.RecordLoginEvent(ctx, authSub, ip, ua)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("setting up auth middleware: %w", err)
	}

	accountMod.MustBeWired()

	return &APIModules{
		Organization: organizationMod,
		Account:      accountMod,
		Reference:    refMod,
		Billing:      billingMod,
		Notification: notifMod,
		AuthMW:       authMW,
		AuthSSEMW:    authSSEMW,
		OrgMW:        middleware.NewOrganizationMiddleware(cachedWSReader),
		RLSMW:        middleware.NewRLSTxMiddleware(infra.Pool),
		MFAMW:        middleware.RequireMFAIfEnabled(accountMod),
	}, nil
}
