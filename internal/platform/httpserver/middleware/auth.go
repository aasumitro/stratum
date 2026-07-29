// Package middleware holds Gin middleware shared across every module:
// auth, organization resolution, rate limiting, request ID, recovery. None of
// this is domain logic — a module's handler.go files import this
// package, but this package never imports a module.
package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/gin-gonic/gin"
	jwtgo "github.com/golang-jwt/jwt/v5"

	"github.com/aasumitro/stratum/internal/platform/config"
)

// authHookConcurrency caps in-flight OnAuth/OnLogin goroutines so a
// request burst can't spawn an unbounded number of them; a saturated pool
// drops (and logs) the invocation rather than queueing, since these hooks
// are best-effort side effects (last_seen_at, login tracking), not
// request-critical work.
const authHookConcurrency = 64

// claimsKey is the gin.Context key auth-validated claims are stored
// under. Use ClaimsFromContext to retrieve them — don't read this key
// directly, so the storage mechanism can change without breaking callers.
const claimsContextKey = "auth.claims"

// Claims is the minimal set of JWT claims this app relies on, plus Raw
// for any module that needs a custom claim (e.g. an org_id set by
// Supabase/Clerk) without us having to grow this struct for every
// possible IdP-specific field.
type Claims struct {
	Subject string // the third-party auth user ID — NOT our internal user.id; modules map this via contracts.UserReader or similar
	Raw     jwtgo.MapClaims
}

// SessionIdentifier extracts the claim this app treats as a per-session
// identifier for revocation purposes. Verified against a real Supabase
// access token (decoded one issued for the test account): Supabase issues
// session_id, not jti — session_id is checked first for that reason. jti is
// checked as a fallback only, for forward compatibility with any other
// JWKS-publishing IdP config.AuthConfig's JWKSURL might someday point at
// that issues one instead. Empty return means neither claim was present —
// revocation checks silently no-op for that token, same fail-open
// convention as the rest of this app's best-effort security checks.
func SessionIdentifier(claims jwtgo.MapClaims) string {
	if sid, _ := claims["session_id"].(string); sid != "" {
		return sid
	}
	sid, _ := claims["jti"].(string)
	return sid
}

// AuthHooks holds optional callbacks invoked by NewAuthMiddleware on each
// authenticated request. All hooks are optional; nil = skip.
type AuthHooks struct {
	// OnAuth is called fire-and-forget (goroutine, 200ms timeout) after a
	// successful authentication. Used to update last_seen_at.
	OnAuth func(ctx context.Context, authSub string)

	// IsRevoked is called synchronously before the request continues, keyed
	// by SessionIdentifier(claims). Return true to block with 401. Used for
	// revoked-token checks.
	IsRevoked func(ctx context.Context, sessionID string) bool

	// OnLogin is called fire-and-forget (goroutine, 200ms timeout) on every
	// authenticated request. Callers are expected to rate-gate internally.
	OnLogin func(ctx context.Context, authSub, ip, ua string)
}

// NewAuthMiddleware builds Gin middleware that validates the
// Authorization: Bearer <token> header against cfg's JWKS endpoint.
// keyfunc.NewDefaultCtx fetches the JWKS once at startup and keeps it
// fresh via a background refresh goroutine (tied to ctx's lifetime) —
// requests never block on a JWKS fetch except possibly the very first
// one if startup hasn't completed yet.
//
// Returns two handlers sharing one JWKS client: authMW (header-only —
// wire this on every normal route) and sseMW (header, falling back to the
// __session cookie — wire this on the SSE stream route only, since
// EventSource can't set headers). Accepting the cookie broadly would widen
// the CSRF/token-exposure surface for no benefit, so it's opt-in per route.
//
// On success, Claims are attached to the request context — retrieve them
// in a handler with ClaimsFromContext(c).
func NewAuthMiddleware(ctx context.Context, cfg config.AuthConfig, hooks ...AuthHooks) (authMW, sseMW gin.HandlerFunc, err error) {
	jwks, err := keyfunc.NewDefaultCtx(ctx, []string{cfg.JWKSURL})
	if err != nil {
		return nil, nil, fmt.Errorf("creating JWKS client: %w", err)
	}

	var h AuthHooks
	if len(hooks) > 0 {
		h = hooks[0]
	}

	hookSem := make(chan struct{}, authHookConcurrency)
	// parent is the request's own context, already detached via
	// context.WithoutCancel by the caller — preserves OTel spans and
	// request-scoped log fields (so hook logs correlate back to the
	// request that triggered them) while not inheriting the request's
	// cancellation, which would otherwise fire the moment the response is
	// written, before this 200ms budget has a chance to run.
	runHook := func(parent context.Context, fn func(ctx context.Context)) {
		select {
		case hookSem <- struct{}{}:
		default:
			slog.Warn("middleware.NewAuthMiddleware: auth hook pool saturated, dropping invocation")
			return
		}
		go func() {
			defer func() { <-hookSem }()
			ctx, cancel := context.WithTimeout(parent, 200*time.Millisecond)
			defer cancel()
			fn(ctx)
		}()
	}

	build := func(allowCookieFallback bool) gin.HandlerFunc {
		return func(c *gin.Context) {
			tokenString, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
			if !ok || strings.TrimSpace(tokenString) == "" {
				if cookie, err := c.Cookie("__session"); allowCookieFallback && err == nil && cookie != "" {
					tokenString = cookie
				} else {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or malformed authorization header"})
					return
				}
			}

			parseOpts := []jwtgo.ParserOption{
				// Required to prevent algorithm-confusion attacks — see
				// https://auth0.com/blog/critical-vulnerabilities-in-json-web-token-libraries/,
				// referenced directly in golang-jwt's own Parse docs. Without
				// this, an attacker could craft a token using a different
				// algorithm than the one your JWKS keys are meant for.
				jwtgo.WithValidMethods([]string{"RS256", "RS384", "RS512", "ES256", "ES384", "ES512"}),
			}
			if cfg.Issuer != "" {
				parseOpts = append(parseOpts, jwtgo.WithIssuer(cfg.Issuer))
			}
			if cfg.Audience != "" {
				// Native option — golang-jwt parses `aud` into jwt.ClaimStrings
				// and checks membership correctly whether the claim is a
				// single string or a JSON array, so there's no need to
				// hand-roll that type-switch ourselves.
				parseOpts = append(parseOpts, jwtgo.WithAudience(cfg.Audience))
			}

			parsedToken, err := jwtgo.Parse(tokenString, jwks.Keyfunc, parseOpts...)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
				return
			}

			claims, ok := parsedToken.Claims.(jwtgo.MapClaims)
			if !ok {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid claims"})
				return
			}

			sub, _ := claims["sub"].(string)
			if sub == "" {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid claims"})
				return
			}

			// Synchronous revocation check — must happen before c.Next().
			if h.IsRevoked != nil {
				sid := SessionIdentifier(claims)
				if sid == "" {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token missing session identifier"})
					return
				}
				if h.IsRevoked(c.Request.Context(), sid) {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token has been revoked"})
					return
				}
			}

			c.Set(claimsContextKey, Claims{
				Subject: sub,
				Raw:     claims,
			})

			if h.OnAuth != nil {
				fn := h.OnAuth
				runHook(context.WithoutCancel(c.Request.Context()), func(ctx context.Context) { fn(ctx, sub) })
			}

			if h.OnLogin != nil {
				fn := h.OnLogin
				ip, ua := c.ClientIP(), c.Request.UserAgent()
				runHook(context.WithoutCancel(c.Request.Context()), func(ctx context.Context) { fn(ctx, sub, ip, ua) })
			}

			c.Next()
		}
	}

	return build(false), build(true), nil
}

// ClaimsFromContext retrieves the Claims attached by NewAuthMiddleware.
// Returns false if called on a route not behind the auth middleware —
// callers must check this rather than assuming claims are always present,
// since some routes (e.g. public lookup/reference endpoints) intentionally
// skip auth.
func ClaimsFromContext(c *gin.Context) (Claims, bool) {
	v, exists := c.Get(claimsContextKey)
	if !exists {
		return Claims{}, false
	}
	claims, ok := v.(Claims)
	return claims, ok
}
