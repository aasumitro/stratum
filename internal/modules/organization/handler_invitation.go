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

type createInvitationRequest struct {
	Email string `json:"email" binding:"required,email"`
	Role  string `json:"role" binding:"required,oneof=admin member"`
}

// createInvitation godoc
// @Summary      Invite a member by email
// @Tags         invitations
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string                    true  "Organization ID"
// @Param        body            body      createInvitationRequest  true  "Invitation fields"
// @Success      201             {object}  response.Payload{data=invitationRecord}
// @Failure      422             {object}  response.Payload  "validation failed or invitee already a member"
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/invitations [post]
func (h *handler) createInvitation(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	var req createInvitationRequest
	if !request.Bind(c, &req) {
		return
	}

	inv, err := h.svc.createInvitation(c.Request.Context(), ws.ID, req.Email, req.Role, reqctx.Subject(c))
	switch {
	case err == nil:
		response.Success(inv).JSON(c, http.StatusCreated)
	case errors.Is(err, ErrInviteeAlreadyMember):
		response.Error("INVITEE_ALREADY_MEMBER", "this person is already a member of the organization").JSON(c, http.StatusUnprocessableEntity)
	default:
		response.FromError(c, err)
	}
}

// listInvitations godoc
// @Summary      List organization invitations
// @Tags         invitations
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=[]invitationRecord}
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/invitations [get]
func (h *handler) listInvitations(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	invitations, err := h.svc.listInvitations(c.Request.Context(), ws.ID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.List(invitations, int64(len(invitations))).JSON(c, http.StatusOK)
}

// revokeInvitation godoc
// @Summary      Revoke an invitation
// @Tags         invitations
// @Security     BearerAuth
// @Param        organizationID  path  string  true  "Organization ID"
// @Param        invitationID    path  string  true  "Invitation ID"
// @Success      204             "no content"
// @Failure      404             {object}  response.Payload  "invitation not found"
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/invitations/{invitationID} [delete]
func (h *handler) revokeInvitation(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	if err := h.svc.revokeInvitation(c.Request.Context(), ws.ID, c.Param("invitationID")); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type acceptInvitationRequest struct {
	Token string `json:"token" binding:"required"`
}

// acceptInvitation godoc
// @Summary      Accept an invitation
// @Description  Accepts an invitation token for the caller's own (authenticated) email; the token must match the caller's email.
// @Tags         invitations
// @Accept       json
// @Security     BearerAuth
// @Param        body  body  acceptInvitationRequest  true  "Invitation token"
// @Success      204   "no content"
// @Failure      422   {object}  response.Payload  "invitation not found, expired, email mismatch, already a member, plan limit reached, or organization not active"
// @Failure      401   {object}  response.Payload  "missing/invalid auth token"
// @Router       /invitations/accept [post]
func (h *handler) acceptInvitation(c *gin.Context) {
	email := reqctx.Email(c)
	emailVerified := reqctx.EmailVerified(c)

	var req acceptInvitationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		request.ValidationError(err).JSON(c, http.StatusUnprocessableEntity)
		return
	}

	result, err := h.svc.acceptInvitation(c.Request.Context(),
		req.Token, reqctx.Subject(c), email, emailVerified)
	switch {
	case err == nil:
		c.Status(http.StatusNoContent)
	case errors.Is(err, ErrInvitationNotFound):
		response.Error("INVITATION_NOT_FOUND", "invitation not found or revoked").JSON(c, http.StatusUnprocessableEntity)
	case errors.Is(err, ErrInvitationExpired):
		response.Error("INVITATION_EXPIRED", "invitation has expired").JSON(c, http.StatusUnprocessableEntity)
	case errors.Is(err, ErrInvitationEmailMismatch):
		response.Error("INVITATION_EMAIL_MISMATCH", "this invitation was sent to a different email address",
			map[string]string{"invited_email": result.InvitedEmail}).JSON(c, http.StatusUnprocessableEntity)
	case errors.Is(err, ErrInvitationAlreadyMember):
		response.Error("INVITATION_ALREADY_MEMBER", "you are already a member of this organization",
			map[string]string{detailKeyOrganizationID: result.OrganizationID}).JSON(c, http.StatusUnprocessableEntity)
	case errors.Is(err, ErrInvitationOrganizationNotActive):
		response.Error("INVITATION_ORGANIZATION_NOT_ACTIVE", "this organization is not currently active",
			map[string]string{detailKeyOrganizationID: result.OrganizationID}).JSON(c, http.StatusUnprocessableEntity)
	case errors.Is(err, ErrPlanLimitReached):
		response.Error("PLAN_LIMIT_REACHED", "organization has reached its member limit").JSON(c, http.StatusUnprocessableEntity)
	default:
		response.Error("INVITATION_INVALID", "invalid or expired invitation").JSON(c, http.StatusUnprocessableEntity)
	}
}

type declineInvitationRequest struct {
	Token string `json:"token" binding:"required"`
}

// declineInvitation godoc
// @Summary      Decline an invitation
// @Description  Declines and deletes an invitation for the caller's own (authenticated) email; the original inviter is notified in-app.
// @Tags         invitations
// @Accept       json
// @Security     BearerAuth
// @Param        body  body  declineInvitationRequest  true  "Invitation token"
// @Success      204   "no content"
// @Failure      422   {object}  response.Payload  "invitation not found, expired, email mismatch, or already a member"
// @Failure      401   {object}  response.Payload  "missing/invalid auth token"
// @Router       /invitations/decline [post]
func (h *handler) declineInvitation(c *gin.Context) {
	email := reqctx.Email(c)
	emailVerified := reqctx.EmailVerified(c)

	var req declineInvitationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		request.ValidationError(err).JSON(c, http.StatusUnprocessableEntity)
		return
	}

	err := h.svc.declineInvitation(c.Request.Context(), req.Token, email, emailVerified)
	switch {
	case err == nil:
		c.Status(http.StatusNoContent)
	case errors.Is(err, ErrInvitationNotFound):
		response.Error("INVITATION_NOT_FOUND", "invitation not found or revoked").JSON(c, http.StatusUnprocessableEntity)
	case errors.Is(err, ErrInvitationExpired):
		response.Error("INVITATION_EXPIRED", "invitation has expired").JSON(c, http.StatusUnprocessableEntity)
	case errors.Is(err, ErrInvitationEmailMismatch):
		response.Error("INVITATION_EMAIL_MISMATCH", "this invitation was sent to a different email address").JSON(c, http.StatusUnprocessableEntity)
	case errors.Is(err, ErrInvitationAlreadyMember):
		response.Error("INVITATION_ALREADY_MEMBER", "you are already a member of this organization").JSON(c, http.StatusUnprocessableEntity)
	default:
		response.Error("INVITATION_INVALID", "invalid or expired invitation").JSON(c, http.StatusUnprocessableEntity)
	}
}

// previewInvitation godoc
// @Summary      Preview an invitation
// @Description  Read-only invitation details (organization name, role, inviter) for the caller's own email, before committing to accept.
// @Tags         invitations
// @Produce      json
// @Security     BearerAuth
// @Param        token  query     string  true  "Invitation token"
// @Success      200    {object}  response.Payload{data=invitationPreview}
// @Failure      422    {object}  response.Payload  "missing token, not found, expired, email mismatch, or already a member"
// @Failure      401    {object}  response.Payload  "missing/invalid auth token"
// @Router       /invitations/preview [get]
func (h *handler) previewInvitation(c *gin.Context) {
	email := reqctx.Email(c)
	emailVerified := reqctx.EmailVerified(c)
	token := c.Query("token")
	if token == "" {
		response.Error("INVITATION_NOT_FOUND", "missing token").JSON(c, http.StatusUnprocessableEntity)
		return
	}

	preview, err := h.svc.previewInvitation(c.Request.Context(), token, email, emailVerified)
	switch {
	case err == nil:
		response.Success(preview).JSON(c, http.StatusOK)
	case errors.Is(err, ErrInvitationNotFound):
		response.Error("INVITATION_NOT_FOUND", "invitation not found or revoked").JSON(c, http.StatusUnprocessableEntity)
	case errors.Is(err, ErrInvitationExpired):
		response.Error("INVITATION_EXPIRED", "invitation has expired").JSON(c, http.StatusUnprocessableEntity)
	case errors.Is(err, ErrInvitationEmailMismatch):
		response.Error("INVITATION_EMAIL_MISMATCH", "this invitation was sent to a different email address",
			map[string]string{"invited_email": preview.InvitedEmail}).JSON(c, http.StatusUnprocessableEntity)
	case errors.Is(err, ErrInvitationAlreadyMember):
		response.Error("INVITATION_ALREADY_MEMBER", "you are already a member of this organization",
			map[string]string{detailKeyOrganizationID: preview.OrganizationID}).JSON(c, http.StatusUnprocessableEntity)
	default:
		response.Error("INVITATION_INVALID", "invalid or expired invitation").JSON(c, http.StatusUnprocessableEntity)
	}
}

// listMyInvitations godoc
// @Summary      List the caller's pending invitations
// @Description  Returns pending invitations sent to the caller's authenticated email, across every organization.
// @Tags         invitations
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.Payload{data=[]myInvitationRecord}
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/invitations [get]
func (h *handler) listMyInvitations(c *gin.Context) {
	email := reqctx.Email(c)
	if email == "" {
		response.List([]myInvitationRecord{}, 0).JSON(c, http.StatusOK)
		return
	}

	invitations, err := h.svc.listMyInvitations(c.Request.Context(), email)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.List(invitations, int64(len(invitations))).JSON(c, http.StatusOK)
}

type requestNewInvitationRequest struct {
	Token string `json:"token" binding:"required"`
}

// requestNewInvitation godoc
// @Summary      Request a fresh invitation
// @Description  Notifies the original inviter that the caller's invitation (identified by an expired/invalid token) needs to be resent — does not resend automatically.
// @Tags         invitations
// @Accept       json
// @Security     BearerAuth
// @Param        body  body  requestNewInvitationRequest  true  "Expired/invalid invitation token"
// @Success      204   "no content"
// @Failure      422   {object}  response.Payload  "validation failed"
// @Failure      401   {object}  response.Payload  "missing/invalid auth token"
// @Router       /invitations/request-new [post]
func (h *handler) requestNewInvitation(c *gin.Context) {
	var req requestNewInvitationRequest
	if !request.Bind(c, &req) {
		return
	}
	email := reqctx.Email(c)
	emailVerified := reqctx.EmailVerified(c)
	if err := h.svc.requestNewInvitation(c.Request.Context(), req.Token, email, emailVerified); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
