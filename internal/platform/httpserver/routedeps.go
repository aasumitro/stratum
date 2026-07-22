package httpserver

import "github.com/gin-gonic/gin"

// RouteDeps bundles the route-group middleware a module's Register may need.
// Built once in api.go and passed to every module's Register, replacing a
// positional gin.HandlerFunc parameter list that grew to 5 args for billing
// alone and drifted independently per module — a field here is named and
// optional, so adding one doesn't reorder every existing call site. A
// module's Register only reads the fields it actually uses.
type RouteDeps struct {
	Auth gin.HandlerFunc
	// AuthSSE is Auth plus a __session cookie fallback — wire this only on
	// the SSE stream route, since EventSource can't set the Authorization
	// header. Every other route must use Auth.
	AuthSSE gin.HandlerFunc
	// RateLimit is per-subject rate limiting — must be wired after Auth (or
	// AuthSSE) in every module's route group, never at the engine/group
	// level ahead of auth, or its key derivation silently falls back to
	// client IP (see middleware.ByAuthenticatedSubject).
	RateLimit   gin.HandlerFunc
	Org         gin.HandlerFunc
	MFA         gin.HandlerFunc
	RLS         gin.HandlerFunc
	Idempotency gin.HandlerFunc
}
