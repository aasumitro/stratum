package middleware

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

// BillingGate returns a middleware that enforces a plan usage limit for the
// given metric. On limit exceeded it responds 429 with X-Usage-Current and
// X-Usage-Limit headers. On lookup error it fails open (passes the request).
// Requires orgMW to have run so OrganizationFromContext is populated.
func BillingGate(billingReader contracts.BillingReader, metric string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ws, ok := OrganizationFromContext(c)
		if !ok {
			c.Next()
			return
		}
		current, limit, err := billingReader.CheckUsageLimit(c.Request.Context(), ws.ID, metric)
		if err != nil {
			c.Next() // fail open on lookup error
			return
		}
		c.Header("X-Usage-Current", strconv.FormatInt(current, 10))
		c.Header("X-Usage-Limit", strconv.Itoa(limit))
		if limit >= 0 && current >= int64(limit) {
			response.Error("PLAN_LIMIT_REACHED", "plan limit reached for "+metric).JSON(c, http.StatusTooManyRequests)
			c.Abort()
			return
		}
		c.Next()
	}
}
