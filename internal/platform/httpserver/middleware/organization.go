package middleware

import (
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/contracts"
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
	if status != "suspended" {
		return false
	}
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
