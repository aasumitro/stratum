package billing

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/httpserver/reqctx"
	"github.com/aasumitro/stratum/internal/platform/httpserver/request"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

// --- addons ---

// swag can only resolve a cross-package type referenced in a @Success
// annotation if the package is imported in the same file — contracts is
// otherwise unused here (listAddonsCatalog infers its type from service.go).
var _ contracts.AddonInfo

type attachAddonRequest struct {
	AddonID  string `json:"addon_id" binding:"required"`
	Quantity int    `json:"quantity" binding:"omitempty,min=1"`
}

// attachAddon godoc
// @Summary      Attach an addon
// @Description  Owner only. Requires step-up MFA (aal2) if the caller has MFA enabled. On a
// @Description  trialing subscription the requested quantity applies immediately. On any other
// @Description  subscription status, an increase (including a brand-new attach) instead creates a
// @Description  day-prorated invoice and returns the addon record with pending_quantity/
// @Description  pending_invoice_id set — the live quantity only rises once that invoice is
// @Description  confirmed paid. A decrease still schedules for the next renewal.
// @Tags         billing
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string               true  "Organization ID"
// @Param        body            body      attachAddonRequest   true  "Addon to attach"
// @Success      200             {object}  response.Payload{data=attachedAddonRecord}
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

	addon, err := h.svc.attachAddon(c.Request.Context(), ws.ID, req.AddonID, quantity, reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(addon).JSON(c, http.StatusOK)
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
		c.Request.Context(), ws.ID, addonID, reqctx.Subject(c),
	); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// undoAddonQuantityChange godoc
// @Summary      Undo a scheduled addon quantity change
// @Description  Owner only. Requires step-up MFA (aal2) if the caller has MFA enabled.
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Param        addonID         path      string  true  "Addon ID"
// @Success      200             {object}  response.Payload{data=attachedAddonRecord}
// @Failure      422             {object}  response.Payload  "nothing scheduled to undo"
// @Failure      403             {object}  response.Payload  "owner role or MFA step-up required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/addons/{addonID}/undo [post]
func (h *handler) undoAddonQuantityChange(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	addonID := c.Param("addonID")

	addon, err := h.svc.undoScheduledAddonQuantityChange(
		c.Request.Context(), ws.ID, addonID, reqctx.Subject(c),
	)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(addon).JSON(c, http.StatusOK)
}

// listAddons godoc
// @Summary      List attached addons
// @Description  Each addon's prices map holds exactly one currency — the subscription's own
// @Description  (sub.Currency), never every currency the catalog stores.
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

// listAddonsCatalog godoc
// @Summary      List the addon catalog, scoped to this subscription's currency
// @Description  Same catalog as GET /references/addons (unattached options, not what's already
// @Description  on this subscription), but prices are scoped to the subscription's own
// @Description  already-fixed currency instead of the caller's GeoIP-resolved one.
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=[]contracts.AddonInfo}
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/addons/catalog [get]
func (h *handler) listAddonsCatalog(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	addons, err := h.svc.listAddonsCatalog(c.Request.Context(), ws.ID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.List(addons, int64(len(addons))).JSON(c, http.StatusOK)
}
