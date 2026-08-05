package organization

import (
	"cmp"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/audit"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/httpserver/reqctx"
	"github.com/aasumitro/stratum/internal/platform/httpserver/request"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

// --- organization routes ---

type createOrganizationRequest struct {
	Slug        string `json:"slug" binding:"required,min=1,max=63"`
	Name        string `json:"name" binding:"required,min=1,max=100"`
	CountryCode string `json:"country_code" binding:"omitempty,len=2"`
	// Plan is required and validated dynamically against the billing
	// catalog (service.createOrganization, via refReader.GetPlanByID) —
	// no oneof here, same reasoning as billing.changePlanRequest.Plan: a
	// static enum would reject any plan an operator adds through Studio.
	// The caller must always choose one explicitly — no silent default.
	Plan string `json:"plan" binding:"required,max=64"`
	// Cycle is required, same reasoning as Plan above — no silent default.
	Cycle string `json:"cycle" binding:"required,oneof=monthly yearly"`
	// Addons and CouponCode are both optional — the "cart" the caller can
	// build alongside plan/cycle so the org's first invoice (or, for a
	// trial, the invoice cut at trial-end) already includes them instead of
	// requiring separate attach/redeem calls raced against async
	// provisioning. Both are validated against the live billing catalog
	// before the organization is written, same as Plan.
	Addons     []createOrganizationAddonRequest `json:"addons,omitempty" binding:"omitempty,dive"`
	CouponCode string                           `json:"coupon_code,omitempty" binding:"omitempty,max=64"`
}

type createOrganizationAddonRequest struct {
	AddonID  string `json:"addon_id" binding:"required"`
	Quantity int    `json:"quantity" binding:"omitempty,min=1"`
}

// createOrganization godoc
// @Summary      Create an organization
// @Description  Creates a new organization owned by the caller. Plan and cycle are required and validated against the billing catalog.
// @Tags         organization
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      createOrganizationRequest              true  "Organization fields"
// @Success      201   {object}  response.Payload{data=organizationRecord}
// @Failure      422   {object}  response.Payload  "validation failed"
// @Failure      401   {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations [post]
func (h *handler) createOrganization(c *gin.Context) {
	var req createOrganizationRequest
	if !request.Bind(c, &req) {
		return
	}
	req.CountryCode = cmp.Or(req.CountryCode, "US")

	addons := make([]addonSelection, len(req.Addons))
	for i, a := range req.Addons {
		quantity := a.Quantity
		if quantity == 0 {
			quantity = 1
		}
		addons[i] = addonSelection{AddonID: a.AddonID, Quantity: quantity}
	}

	t, err := h.svc.createOrganization(c.Request.Context(), req.Slug, req.Name,
		reqctx.Subject(c), req.CountryCode, req.Plan, req.Cycle, addons, req.CouponCode)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(t).JSON(c, http.StatusCreated)
}

// listOrganizations godoc
// @Summary      List the caller's organizations
// @Description  Returns every organization the caller is a member of, with their role.
// @Tags         organization
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.Payload{data=[]organizationView}
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations [get]
func (h *handler) listOrganizations(c *gin.Context) {
	organizations, err := h.svc.listOrganizations(c.Request.Context(), reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.List(organizations, int64(len(organizations))).JSON(c, http.StatusOK)
}

// getOrganization godoc
// @Summary      Get an organization
// @Tags         organization
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=organizationRecord}
// @Failure      404             {object}  response.Payload  "organization not found"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID} [get]
func (h *handler) getOrganization(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	t, err := h.svc.getOrganization(c.Request.Context(), ws.ID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(t).JSON(c, http.StatusOK)
}

type updateOrganizationRequest struct {
	Name string `json:"name" binding:"omitempty,min=1,max=100"`
	Slug string `json:"slug" binding:"omitempty,min=1,max=63"`
}

// updateOrganization godoc
// @Summary      Update an organization
// @Description  Updates name and/or slug. Owner only.
// @Tags         organization
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string                     true  "Organization ID"
// @Param        body            body      updateOrganizationRequest  true  "Fields to update"
// @Success      200             {object}  response.Payload{data=organizationRecord}
// @Failure      422             {object}  response.Payload  "validation failed"
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID} [patch]
func (h *handler) updateOrganization(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	var req updateOrganizationRequest
	if !request.Bind(c, &req) {
		return
	}
	if req.Name == "" && req.Slug == "" {
		response.Error("VALIDATION_FAILED", "at least one of name or slug must be provided").JSON(c, http.StatusUnprocessableEntity)
		return
	}

	t, err := h.svc.updateOrganization(c.Request.Context(), ws.ID, req.Name, req.Slug)
	if err != nil {
		response.FromError(c, err)
		return
	}
	h.invalidateOrg(c, ws.ID)
	response.Success(t).JSON(c, http.StatusOK)
}

// deleteOrganization godoc
// @Summary      Delete an organization
// @Description  Owner only. Requires step-up MFA (aal2) if the caller has MFA enabled.
// @Tags         organization
// @Security     BearerAuth
// @Param        organizationID  path  string  true  "Organization ID"
// @Success      204             "no content"
// @Failure      403             {object}  response.Payload  "owner role or MFA step-up required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID} [delete]
func (h *handler) deleteOrganization(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	if err := h.svc.deleteOrganization(c.Request.Context(), ws.ID); err != nil {
		response.FromError(c, err)
		return
	}
	h.invalidateOrg(c, ws.ID)
	c.Status(http.StatusNoContent)
}

type suspendOrganizationRequest struct {
	Reason string `json:"reason" binding:"omitempty,max=500"`
}

// suspendOrganization is the owner-facing self-service pause (the Danger
// zone "Suspend organization" card) — reversible, read-only for everyone
// including the owner while active. Distinct trigger from Stratum Studio's
// operator-driven suspend and billing's auto-suspend-on-expiry, but the
// same `organizations.status` field and repository methods — an owner who
// self-suspends can self-unsuspend any time, no different from a billing
// suspension being cleared by payment.
//
// @Summary      Suspend an organization
// @Description  Owner-facing self-service pause — reversible, read-only for everyone including the owner while active. Requires step-up MFA (aal2) if the caller has MFA enabled.
// @Tags         organization
// @Accept       json
// @Security     BearerAuth
// @Param        organizationID  path  string                      true   "Organization ID"
// @Param        body            body  suspendOrganizationRequest  false  "Optional reason"
// @Success      204             "no content"
// @Failure      403             {object}  response.Payload  "owner role or MFA step-up required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/suspend [post]
func (h *handler) suspendOrganization(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	var req suspendOrganizationRequest
	if !request.Bind(c, &req) {
		return
	}
	reason := req.Reason
	if reason == "" {
		reason = suspendReasonSelfService
	}

	if err := h.svc.suspendOrganization(c.Request.Context(), ws.ID, reason); err != nil {
		response.FromError(c, err)
		return
	}
	h.invalidateOrg(c, ws.ID)
	c.Status(http.StatusNoContent)
}

// unsuspendOrganization godoc
// @Summary      Unsuspend an organization
// @Description  Reverses a self-service suspension. Owner only, requires step-up MFA (aal2) if the caller has MFA enabled.
// @Tags         organization
// @Security     BearerAuth
// @Param        organizationID  path  string  true  "Organization ID"
// @Success      204             "no content"
// @Failure      403             {object}  response.Payload  "owner role or MFA step-up required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/unsuspend [post]
func (h *handler) unsuspendOrganization(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	if err := h.svc.selfUnsuspendOrganization(c.Request.Context(), ws.ID); err != nil {
		response.FromError(c, err)
		return
	}
	h.invalidateOrg(c, ws.ID)
	c.Status(http.StatusNoContent)
}

type updateSettingsRequest struct {
	Timezone   string   `json:"timezone" binding:"required"`
	Locale     string   `json:"locale" binding:"required"`
	AllowedIPs []string `json:"allowed_ips"`
}

// updateSettings godoc
// @Summary      Update organization settings
// @Description  Updates timezone, locale, and the IP allowlist. Owner only.
// @Tags         organization
// @Accept       json
// @Security     BearerAuth
// @Param        organizationID  path  string                 true  "Organization ID"
// @Param        body            body  updateSettingsRequest  true  "Settings fields"
// @Success      204             "no content"
// @Failure      422             {object}  response.Payload  "validation failed"
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/settings [patch]
func (h *handler) updateSettings(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	audit.SetBefore(c, map[string]any{"timezone": ws.Timezone, "locale": ws.Locale, settingAllowedIPs: ws.AllowedIPs})

	var req updateSettingsRequest
	if !request.Bind(c, &req) {
		return
	}

	if err := h.svc.updateSettings(c.Request.Context(), ws.ID, req.Timezone, req.Locale, c.ClientIP(), req.AllowedIPs); err != nil {
		response.FromError(c, err)
		return
	}
	audit.SetAfter(c, map[string]any{"timezone": req.Timezone, "locale": req.Locale, settingAllowedIPs: req.AllowedIPs})
	h.invalidateOrg(c, ws.ID)
	c.Status(http.StatusNoContent)
}

// uploadLogo godoc
// @Summary      Upload organization logo
// @Description  Admin/owner only. Max 2MB, image/* content type.
// @Tags         organization
// @Accept       multipart/form-data
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Param        logo            formData  file    true  "Logo image file"
// @Success      204             "no content"
// @Failure      422             {object}  response.Payload  "missing file, too large, or not an image"
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/logo [post]
func (h *handler) uploadLogo(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	file, header, err := c.Request.FormFile("logo")
	if err != nil {
		response.Error("LOGO_MISSING", "logo file is required").JSON(c, http.StatusUnprocessableEntity)
		return
	}
	defer file.Close()

	if header.Size > maxLogoSize {
		response.Error("LOGO_TOO_LARGE", "logo must be under 2 MB").JSON(c, http.StatusUnprocessableEntity)
		return
	}
	ct, ok := request.SniffImageType(file)
	if !ok {
		response.Error("LOGO_INVALID_TYPE", "logo must be an image").JSON(c, http.StatusUnprocessableEntity)
		return
	}

	if err := h.svc.uploadLogo(c.Request.Context(), ws.ID, file, ct); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
