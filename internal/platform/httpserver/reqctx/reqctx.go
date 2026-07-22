// Package reqctx provides thin accessors over per-request auth state so
// handlers don't need to import the middleware package directly just to read
// the caller's subject.
package reqctx

import (
	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
)

// Subject returns the authenticated caller's subject (empty on an unauthed
// route). Auth middleware already rejects an empty sub claim, so any authed
// route is guaranteed a non-empty value.
func Subject(c *gin.Context) string {
	claims, _ := middleware.ClaimsFromContext(c)
	return claims.Subject
}

// Email returns the authenticated caller's email claim (empty if absent —
// phone/anonymous/SSO-without-email signups carry no email claim at all).
func Email(c *gin.Context) string {
	claims, _ := middleware.ClaimsFromContext(c)
	email, _ := claims.Raw["email"].(string)
	return email
}

// EmailVerified reports whether the caller's email is verified. Supabase
// never sets a top-level "email_verified" claim — it nests the flag under
// user_metadata, and reserves that key server-side (a client-side
// updateUser call can't overwrite it), so it's safe to trust here.
func EmailVerified(c *gin.Context) bool {
	claims, _ := middleware.ClaimsFromContext(c)
	userMetadata, _ := claims.Raw["user_metadata"].(map[string]any)
	verified, _ := userMetadata["email_verified"].(bool)
	return verified
}
