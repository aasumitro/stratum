package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

// RequireMFAIfEnabled returns middleware that enforces a verified second
// factor (aal2) only for callers who have opted into MFA. Users who never
// enrolled a factor always sit at aal1 — that's their ceiling, not a
// shortfall, so they pass through unchanged. Only once a user has a
// verified factor does aal1 become insufficient for the gated route.
//
// Unlike BillingGate (a plan-limit check, where fail-open just risks a
// missed upsell), a lookup error here fails closed: this gate protects the
// highest-impact actions (delete account, transfer ownership, plan/addon
// changes), so an IsMFAEnabled error must not silently grant access to a
// caller who may have MFA enabled but whose status we can't currently
// verify. 503, not 403 — this isn't "you lack a verified factor," it's "we
// can't tell right now," and the frontend shouldn't prompt for step-up MFA
// on a transient backend error.
func RequireMFAIfEnabled(userReader contracts.UserReader) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFromContext(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or malformed authorization header"})
			return
		}

		enabled, err := userReader.IsMFAEnabled(c.Request.Context(), claims.Subject)
		if err != nil {
			response.Error("MFA_STATUS_UNAVAILABLE", "could not verify your account's MFA status, try again").
				JSON(c, http.StatusServiceUnavailable)
			c.Abort()
			return
		}
		if !enabled {
			c.Next()
			return
		}

		if aal, _ := claims.Raw["aal"].(string); aal != "aal2" {
			response.Error("MFA_REQUIRED", "this action requires a verified second factor").
				JSON(c, http.StatusForbidden)
			c.Abort()
			return
		}
		c.Next()
	}
}
