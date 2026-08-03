package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/cache"
)

const (
	// trialRateLimit is the requests-per-minute ceiling while an
	// organization's subscription is trialing, applied regardless of which
	// plan is being trialed. A trial isn't a catalog plan (no billing.plans
	// row of its own), so this stays a small hardcoded constant rather
	// than a catalog lookup.
	trialRateLimit = 60

	// planRateLimitCacheTTL caches a plan's resolved api_rate_limit value
	// before re-reading the catalog — this middleware runs on every
	// request, so a Postgres round trip per request isn't acceptable.
	// Mirrors the existing 30s RBAC-role-cache TTL convention
	// (platform/cache/organization.go).
	planRateLimitCacheTTL = 30 * time.Second
)

// RateLimitCatalog supplies the dependencies needed to derive an
// organization-scoped request's rate limit from its subscription's plan via
// the billing catalog's api_rate_limit feature (billing.plan_features,
// type='config').
// All three fields are used together; pass a zero-value RateLimitCatalog
// (or omit the variadic argument entirely) to skip catalog-based limiting
// and always fall back to defaultLimit.
type RateLimitCatalog struct {
	Billing contracts.BillingReader
	Catalog contracts.CatalogReader
	Cache   *cache.Namespace
}

type apiRateLimitConfig struct {
	RequestsPerMinute int `json:"requests_per_minute"`
}

// resolvePlanRate looks up plan's api_rate_limit catalog feature
// (config_value: {"requests_per_minute": N}, N=-1 meaning unlimited),
// caching the resolved value in Redis for planRateLimitCacheTTL. Returns
// ok=false if the plan can't be resolved or has no api_rate_limit
// entitlement — callers fall back to defaultLimit either way (fail open,
// the same convention every other billing-gate check in this codebase
// already uses).
func (rc RateLimitCatalog) resolvePlanRate(ctx context.Context, plan string) (rate int, ok bool) {
	if rc.Cache != nil {
		if cached, err := rc.Cache.Get(ctx, plan); err == nil {
			if parsed, err := strconv.Atoi(cached); err == nil {
				return parsed, true
			}
		}
	}

	info, err := rc.Catalog.GetPlanByID(ctx, plan)
	if err != nil {
		return 0, false
	}

	raw, exists := info.ConfigValues["api_rate_limit"]
	if !exists {
		return 0, false
	}
	var cfg apiRateLimitConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return 0, false
	}

	if rc.Cache != nil {
		_ = rc.Cache.Set(ctx, plan, strconv.Itoa(cfg.RequestsPerMinute), planRateLimitCacheTTL)
	}
	return cfg.RequestsPerMinute, true
}

// KeyFunc extracts the rate-limit key from a request — e.g. client IP
// for public/unauthenticated routes, or the authenticated user's
// Subject for routes behind NewAuthMiddleware. Keeping this pluggable
// (rather than hardcoding IP-based limiting) is what lets a webhook
// route limit by source IP while an API route limits per-user.
type KeyFunc func(c *gin.Context) string

// ByClientIP is a KeyFunc that limits per client IP — the right default
// for public routes with no auth (e.g. payment webhooks from Stripe/Xendit,
// where there's no per-user identity to key on).
func ByClientIP(c *gin.Context) string {
	return c.ClientIP()
}

// ByAuthenticatedSubject is a KeyFunc that limits per authenticated user.
// Must run after NewAuthMiddleware in the chain — falls back to client IP
// if no claims are present (defensive: better to rate-limit by something
// than to silently skip limiting if this is ever misordered).
func ByAuthenticatedSubject(c *gin.Context) string {
	if claims, ok := ClaimsFromContext(c); ok && claims.Subject != "" {
		return claims.Subject
	}
	return c.ClientIP()
}

// NewRateLimitMiddleware builds Gin middleware enforcing limit per key
// (as produced by keyFunc), scoped under namespace — callers should pass
// a distinct namespace per route group so two rate-limited routes never
// share a counter even if their keys happen to collide.
//
// When catalog is provided, organization-scoped requests (routes with a
// :organizationID param) derive their limit from the organization's
// subscription: trialRateLimit while trialing, otherwise the plan's
// api_rate_limit catalog feature. Fails open to defaultLimit on any
// lookup error.
func NewRateLimitMiddleware(limiter *cache.RateLimiter, keyFunc KeyFunc, defaultLimit cache.Limit, catalog ...RateLimitCatalog) gin.HandlerFunc {
	var rc RateLimitCatalog
	if len(catalog) > 0 {
		rc = catalog[0]
	}

	return func(c *gin.Context) {
		limit := defaultLimit
		orgID := c.Param("organizationID")

		if rc.Billing != nil && rc.Catalog != nil && orgID != "" {
			if sub, err := rc.Billing.GetSubscriptionBySubject(
				c.Request.Context(), "organization", orgID,
			); err == nil {
				switch sub.Status {
				case "trialing":
					limit = cache.PerMinute(trialRateLimit)
				default:
					if rate, ok := rc.resolvePlanRate(c.Request.Context(), sub.Plan); ok {
						if rate < 0 { // unlimited plan
							c.Next()
							return
						}
						limit = cache.PerMinute(rate)
					}
				}
			}
		}

		// The limit above is resolved per-org (its plan's api_rate_limit),
		// so the counter must be too — otherwise a user in both a
		// low-plan and a high-plan org shares one counter across both,
		// letting the low-plan org's traffic consume the high-plan org's
		// budget (or vice versa). Non-org routes keep the plain key.
		key := keyFunc(c)
		if orgID != "" {
			key += ":" + orgID
		}
		result, err := limiter.Allow(c.Request.Context(), key, limit)
		if err != nil {
			// Fail open: a Redis blip shouldn't take down the whole API.
			c.Next()
			return
		}

		c.Header("X-RateLimit-Limit", strconv.Itoa(limit.Rate))
		c.Header("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
		c.Header("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(result.ResetAfter).Unix(), 10))

		if !result.Allowed {
			c.Header("Retry-After", strconv.Itoa(int(result.RetryAfter.Seconds())))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "rate limit exceeded",
				"retry_after": result.RetryAfter.String(),
			})
			return
		}

		c.Next()
	}
}
