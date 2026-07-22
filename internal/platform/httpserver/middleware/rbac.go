package middleware

import (
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
)

const memberRoleContextKey = "organization.role"

// RequireRole returns middleware that checks the caller's organization role
// against the allowed roles. The role is populated by NewOrganizationMiddleware.
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role, ok := MemberRoleFromContext(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			return
		}
		if slices.Contains(roles, role) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
	}
}

// MemberRoleFromContext retrieves the caller's role in the current organization.
func MemberRoleFromContext(c *gin.Context) (string, bool) {
	v, exists := c.Get(memberRoleContextKey)
	if !exists {
		return "", false
	}
	role, ok := v.(string)
	return role, ok
}
