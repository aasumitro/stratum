package organization

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/httpserver/reqctx"
	"github.com/aasumitro/stratum/internal/platform/httpserver/request"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

// regenerateInviteCode godoc
// @Summary      Regenerate the invite code
// @Description  Owner only. Invalidates the previous code.
// @Tags         organization
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=object{invite_code=string}}
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/invite-code [post]
func (h *handler) regenerateInviteCode(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	// Semantic audit label so this rotation is findable by intent, not just
	// as a bare POST to the invite-code route.
	c.Set("audit.action", "invite_code.regenerated")

	code, err := h.svc.regenerateInviteCode(c.Request.Context(), ws.ID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(gin.H{"invite_code": code}).JSON(c, http.StatusOK)
}

type toggleInviteCodeRequest struct {
	Enabled bool `json:"enabled"`
}

// toggleInviteCode godoc
// @Summary      Enable or disable the invite code
// @Description  Owner only.
// @Tags         organization
// @Accept       json
// @Security     BearerAuth
// @Param        organizationID  path  string                    true  "Organization ID"
// @Param        body            body  toggleInviteCodeRequest  true  "Enabled flag"
// @Success      204             "no content"
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/invite-code [patch]
func (h *handler) toggleInviteCode(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	var req toggleInviteCodeRequest
	if !request.Bind(c, &req) {
		return
	}

	if err := h.svc.toggleInviteCode(c.Request.Context(), ws.ID, req.Enabled); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// previewInviteCode godoc
// @Summary      Preview an organization by invite code
// @Description  Read-only organization + owner details for a code, before committing to join.
// @Tags         organization
// @Produce      json
// @Security     BearerAuth
// @Param        code  query     string  true  "Invite code"
// @Success      200   {object}  response.Payload{data=inviteCodePreview}
// @Failure      422   {object}  response.Payload  "missing code, invalid code, invite code disabled, or already a member"
// @Failure      401   {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/join/preview [get]
func (h *handler) previewInviteCode(c *gin.Context) {
	code := c.Query("code")
	if code == "" {
		response.Error("INVITE_CODE_INVALID", "invalid or disabled invite code").JSON(c, http.StatusUnprocessableEntity)
		return
	}

	preview, err := h.svc.previewInviteCode(c.Request.Context(), code, reqctx.Subject(c))
	switch {
	case err == nil:
		response.Success(preview).JSON(c, http.StatusOK)
	case errors.Is(err, ErrJoinAlreadyMember):
		response.Error("JOIN_ALREADY_MEMBER", "you are already a member of this organization",
			map[string]string{detailKeyOrganizationID: preview.OrganizationID}).JSON(c, http.StatusUnprocessableEntity)
	default:
		response.FromError(c, err)
	}
}

type joinByCodeRequest struct {
	Code string `json:"code" binding:"required"`
}

// joinByCode godoc
// @Summary      Join an organization by invite code
// @Tags         organization
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      joinByCodeRequest  true  "Invite code"
// @Success      200   {object}  response.Payload{data=organizationRecord}
// @Failure      422   {object}  response.Payload  "validation failed, invalid code, invite code disabled, already a member, or plan limit reached"
// @Failure      401   {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/join [post]
func (h *handler) joinByCode(c *gin.Context) {
	var req joinByCodeRequest
	if !request.Bind(c, &req) {
		return
	}

	t, err := h.svc.joinByCode(c.Request.Context(), req.Code, reqctx.Subject(c))
	switch {
	case err == nil:
		response.Success(t).JSON(c, http.StatusOK)
	case errors.Is(err, ErrJoinAlreadyMember):
		response.Error("JOIN_ALREADY_MEMBER", "you are already a member of this organization").JSON(c, http.StatusUnprocessableEntity)
	case errors.Is(err, ErrPlanLimitReached):
		response.Error("PLAN_LIMIT_REACHED", "organization has reached its member limit").JSON(c, http.StatusUnprocessableEntity)
	default:
		response.FromError(c, err)
	}
}
