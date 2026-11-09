package middleware

import (
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/db"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

const organizationContextKey = "organization.organization"

// isSuspensionExempt reports whether route must stay reachable even while
// an organization is suspended — otherwise a suspended organization could
// never be reversed, and its read-only shell (members, files, etc.) would
// be unnavigable instead of just blocked from new writes. Only applies to
// "suspended": a "deleted" organization gets none of these exemptions, it
// stays fully inaccessible. GET matters because without it, self-suspend
// followed by self-unsuspend deadlocks: the unsuspend request itself would
// be rejected by this same active-only check it exists to clear. Matched
// by suffix, not full path, since the
// route's mount prefix differs between the real app (/api/v1) and this
// module's own test harness (/api).
func isSuspensionExempt(c *gin.Context, status string) bool {
	return status == "suspended" && staysOpenWhileBlocked(c)
}

// staysOpenWhileBlocked is the part of an organization that stays reachable when
// billing or a suspension blocks the rest: what it takes to read it, pay for it,
// lift the block, or delete it.
func staysOpenWhileBlocked(c *gin.Context) bool {
	if c.Request.Method == http.MethodGet {
		return true
	}
	route := c.FullPath()
	return strings.HasSuffix(route, "/unsuspend") || strings.Contains(route, "/billing") ||
		(c.Request.Method == http.MethodDelete && strings.HasSuffix(route, "/:organizationID"))
}

func organizationIDExtractor(c *gin.Context) string {
	if id := c.Param("organizationID"); id != "" {
		return id
	}
	return c.GetHeader("X-Organization-ID")
}

// NewOrganizationMiddleware resolves the organization from the request path param
// (:organizationID) or X-Organization-ID header, validates it is active, and
// injects it into context for handlers via OrganizationFromContext.
func NewOrganizationMiddleware(reader contracts.OrganizationReader) gin.HandlerFunc {
	return func(c *gin.Context) {
		organizationID := organizationIDExtractor(c)
		if organizationID == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "missing organization id"})
			return
		}

		ws, err := reader.GetOrganizationByID(c.Request.Context(), organizationID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "organization not found"})
			return
		}

		if ws.Status != "active" && !isSuspensionExempt(c, ws.Status) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "organization is not active"})
			return
		}

		claims, ok := ClaimsFromContext(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		role, err := reader.GetMemberRole(c.Request.Context(), organizationID, claims.Subject)
		if err != nil || role == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "not a member of this organization"})
			return
		}

		if len(ws.AllowedIPs) > 0 && !ipAllowed(c.ClientIP(), ws.AllowedIPs) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "access denied from this IP address"})
			return
		}

		c.Set(organizationContextKey, *ws)
		c.Set(memberRoleContextKey, role)
		// Bind the request's database scope to this authorization decision:
		// db.WithOrgTx refuses to scope a transaction to any other
		// organization for the rest of the request.
		c.Request = c.Request.WithContext(db.WithAuthorizedOrg(c.Request.Context(), ws.ID))
		c.Next()
	}
}

// NewActivationGateMiddleware is the server side of the unpaid activation rule: while an
// organization's first invoice is unpaid, the period in use was never paid for, so writes
// are refused for every member and every client, with the same exemptions as a suspension.
// Mount it after the organization middleware, so a non-member learns nothing from it, and
// after the rate limiter, since it costs a transaction. It reads only for requests it could
// refuse. There is no cache, so paying lifts it on the next request. A failed read answers
// 503: this guards money, so it must not open on an error, and the caller has done nothing
// wrong. RequireMFAIfEnabled makes the same split. Leaving stays open too: a member has no
// say over the invoice, and leaving uses nothing that was not paid for.
func NewActivationGateMiddleware(gate contracts.ActivationGate) gin.HandlerFunc {
	return func(c *gin.Context) {
		if staysOpenWhileBlocked(c) || strings.HasSuffix(c.FullPath(), "/leave") {
			c.Next()
			return
		}
		ws, ok := OrganizationFromContext(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "missing organization"})
			return
		}
		unpaid, err := gate.HasUnpaidActivation(c.Request.Context(), ws.ID)
		if err != nil {
			response.Error("BILLING_STATUS_UNAVAILABLE", "could not verify this organization's billing status, try again").
				JSON(c, http.StatusServiceUnavailable)
			c.Abort()
			return
		}
		if unpaid {
			response.Error("ACTIVATION_UNPAID", "this organization's first invoice is unpaid; the owner can pay it under Billing").
				JSON(c, http.StatusForbidden)
			c.Abort()
			return
		}
		c.Next()
	}
}

// ipAllowed returns true if clientIP is covered by any entry in allowedIPs.
// Entries may be plain IPs or CIDR blocks.
func ipAllowed(clientIP string, allowedIPs []string) bool {
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}
	for _, entry := range allowedIPs {
		if IPEntryMatches(ip, entry) {
			return true
		}
	}
	return false
}

// IPEntryMatches reports whether ip is covered by a single allowlist entry —
// a CIDR block or a bare IP. Bare-IP entries are compared with net.IP.Equal
// rather than raw string equality, so equivalent representations of the same
// address (e.g. an IPv4-mapped IPv6 form) still match. Exported because it's
// the one place this comparison is defined — the organization module's
// pre-save "would this lock me out" check calls it too, so the two can never
// disagree about what "matches" means.
func IPEntryMatches(ip net.IP, entry string) bool {
	if _, network, err := net.ParseCIDR(entry); err == nil {
		return network.Contains(ip)
	}
	entryIP := net.ParseIP(entry)
	return entryIP != nil && entryIP.Equal(ip)
}

// OrganizationFromContext retrieves the OrganizationInfo injected by NewOrganizationMiddleware.
func OrganizationFromContext(c *gin.Context) (contracts.OrganizationInfo, bool) {
	v, exists := c.Get(organizationContextKey)
	if !exists {
		return contracts.OrganizationInfo{}, false
	}
	ws, ok := v.(contracts.OrganizationInfo)
	return ws, ok
}
