package billing

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/httpserver/request"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

// getUsage godoc
// @Summary      Get metered usage
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=[]usageRecord}
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/usage [get]
func (h *handler) getUsage(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	usage, err := h.svc.getUsage(c.Request.Context(), "organization", ws.ID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.List(usage, int64(len(usage))).JSON(c, http.StatusOK)
}

type recordUsageRequest struct {
	Metric string `json:"metric" binding:"required"`
	Value  int64  `json:"value" binding:"required,min=1"`
}

// recordUsage godoc
// @Summary      Record a manual usage correction
// @Description  Owner only.
// @Tags         billing
// @Accept       json
// @Security     BearerAuth
// @Param        organizationID  path  string              true  "Organization ID"
// @Param        body            body  recordUsageRequest  true  "Usage to record"
// @Success      204             "no content"
// @Failure      422             {object}  response.Payload  "validation failed"
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/usage [post]
func (h *handler) recordUsage(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	var req recordUsageRequest
	if !request.Bind(c, &req) {
		return
	}

	if err := h.svc.recordUsage(
		c.Request.Context(), ws.ID, req.Metric, req.Value,
	); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// listFeatures godoc
// @Summary      List resolved feature entitlements
// @Description  Returns the organization's effective feature limits (plan limit + addon deltas) and current usage.
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=[]entitlementRecord}
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/features [get]
func (h *handler) listFeatures(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	entitlements, err := h.svc.resolveEntitlements(c.Request.Context(), ws.ID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(entitlements).JSON(c, http.StatusOK)
}
