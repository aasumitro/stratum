package billing

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/httpserver/request"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

// --- addons ---

type attachAddonRequest struct {
	AddonID  string `json:"addon_id" binding:"required"`
	Quantity int    `json:"quantity" binding:"omitempty,min=1"`
}

// attachAddon godoc
// @Summary      Attach an addon
// @Description  Owner only. Requires step-up MFA (aal2) if the caller has MFA enabled.
// @Tags         billing
// @Accept       json
// @Security     BearerAuth
// @Param        organizationID  path  string               true  "Organization ID"
// @Param        body            body  attachAddonRequest   true  "Addon to attach"
// @Success      204             "no content"
// @Failure      422             {object}  response.Payload  "validation failed"
// @Failure      403             {object}  response.Payload  "owner role or MFA step-up required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/addons [post]
func (h *handler) attachAddon(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	var req attachAddonRequest
	if !request.Bind(c, &req) {
		return
	}
	quantity := req.Quantity
	if quantity == 0 {
		quantity = 1
	}

	if err := h.svc.attachAddon(
		c.Request.Context(), ws.ID, req.AddonID, quantity,
	); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// detachAddon godoc
// @Summary      Detach an addon
// @Description  Owner only. Requires step-up MFA (aal2) if the caller has MFA enabled.
// @Tags         billing
// @Security     BearerAuth
// @Param        organizationID  path  string  true  "Organization ID"
// @Param        addonID         path  string  true  "Addon ID"
// @Success      204             "no content"
// @Failure      403             {object}  response.Payload  "owner role or MFA step-up required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/addons/{addonID} [delete]
func (h *handler) detachAddon(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	addonID := c.Param("addonID")

	if err := h.svc.detachAddon(
		c.Request.Context(), ws.ID, addonID,
	); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// listAddons godoc
// @Summary      List attached addons
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=[]attachedAddonRecord}
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/addons [get]
func (h *handler) listAddons(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	addons, err := h.svc.listAddons(c.Request.Context(), ws.ID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.List(addons, int64(len(addons))).JSON(c, http.StatusOK)
}
