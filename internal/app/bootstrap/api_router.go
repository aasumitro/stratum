package bootstrap

import (
	"fmt"

	"github.com/aasumitro/stratum/internal/platform/audit"
	"github.com/aasumitro/stratum/internal/platform/cache"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
)

// APIRouter is the API's running HTTP surface: the stdlib *http.Server
// wrapper (via httpserver.Server) and the audit writer whose background
// flush loop must be stopped on shutdown.
type APIRouter struct {
	Server      *httpserver.Server
	AuditWriter *audit.Writer
}

// NewAPIRouter builds the gin engine (CORS, body-size limit, health,
// rate limiting, audit logging), mounts every module's routes plus the
// public webhook routes, and wraps it in an httpserver.Server ready to
// Run/Shutdown.
func NewAPIRouter(infra *Infra, mods *APIModules) (*APIRouter, error) {
	cfg := infra.Cfg

	apiRateMW := middleware.NewRateLimitMiddleware(
		cache.NewRateLimiter(infra.Redis, "api"),
		middleware.ByAuthenticatedSubject,
		cache.PerMinute(300),
		middleware.RateLimitCatalog{
			Billing: mods.Billing,
			Catalog: mods.Billing,
			Cache:   cache.NewNamespace(infra.Redis, "ratelimit"),
		},
	)
	ginMode := "release"
	if cfg.Env == EnvDevelopment {
		ginMode = "debug"
	}

	auditWriter := audit.NewWriter(infra.Pool, infra.Log, cfg.AuditRetentionDays)

	corsMW, err := middleware.NewCORSMiddleware(middleware.CORSConfig{
		AllowedOrigins: middleware.ParseCORSOrigins(cfg.CORSOrigins),
		IsDevelopment:  cfg.Env == EnvDevelopment,
	})
	if err != nil {
		auditWriter.Stop()
		return nil, fmt.Errorf("configuring cors: %w", err)
	}

	engine, err := httpserver.New(cfg.ServiceName, infra.Log, ginMode, cfg.TrustedProxies)
	if err != nil {
		auditWriter.Stop()
		return nil, fmt.Errorf("configuring http server: %w", err)
	}
	engine.Use(corsMW, middleware.SecureHeaders(), middleware.MaxBodySize(52<<20)) // 52 MB > 50 MB file cap
	httpserver.RegisterHealth(engine, httpserver.HealthDeps{Pool: infra.Pool,
		Redis: infra.Redis, MQ: infra.MQConn, StatsToken: cfg.StatsToken})
	if cfg.Env == EnvDevelopment {
		httpserver.RegisterSwagger(engine)
	}

	idempotencyMW := middleware.NewIdempotencyMiddleware(cache.NewNamespace(infra.Redis, "billing"))

	routeDeps := httpserver.RouteDeps{
		Auth:    mods.AuthMW,
		AuthSSE: mods.AuthSSEMW,
		// RateLimit is wired here, not at the v1 group level, so it runs
		// after each module's Auth/AuthSSE — see RouteDeps.RateLimit.
		RateLimit:   apiRateMW,
		Org:         mods.OrgMW,
		MFA:         mods.MFAMW,
		RLS:         mods.RLSMW,
		Idempotency: idempotencyMW,
	}

	v1 := engine.Group("/api/v1")
	v1.Use(auditWriter.Middleware())
	mods.Organization.Register(v1, routeDeps)
	mods.Account.Register(v1, routeDeps)
	mods.Reference.Register(v1, routeDeps)
	mods.Billing.Register(v1, routeDeps)
	mods.Notification.Register(v1, routeDeps)

	webhooks := engine.Group("/webhooks")
	if err := mods.Billing.RegisterWebhooks(webhooks); err != nil {
		auditWriter.Stop()
		return nil, fmt.Errorf("registering billing webhooks: %w", err)
	}
	mods.Account.RegisterWebhooks(webhooks)

	return &APIRouter{
		Server:      httpserver.NewServer(engine, cfg.Port),
		AuditWriter: auditWriter,
	}, nil
}
