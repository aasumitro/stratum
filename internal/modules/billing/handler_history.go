package billing

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

// listHistory godoc
// @Summary      List subscription history
// @Description  Returns the audit trail of subscription changes (plan changes, cancellations, etc.) for the organization.
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=[]historyView}
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/history [get]
func (h *handler) listHistory(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	history, err := h.svc.listHistory(
		c.Request.Context(), "organization", ws.ID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.List(history, int64(len(history))).JSON(c, http.StatusOK)
}

// listPayments godoc
// @Summary      List payments
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=[]paymentRecord}
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/payments [get]
func (h *handler) listPayments(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	payments, err := h.svc.listPayments(
		c.Request.Context(), "organization", ws.ID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.List(payments, int64(len(payments))).JSON(c, http.StatusOK)
}
