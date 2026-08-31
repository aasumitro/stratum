package organization

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/httpserver/reqctx"
	"github.com/aasumitro/stratum/internal/platform/httpserver/request"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

// --- member routes ---

// listMembers godoc
// @Summary      List organization members
// @Tags         organization
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=[]memberView}
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/members [get]
func (h *handler) listMembers(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	members, err := h.svc.listMembers(c.Request.Context(), ws.ID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.List(members, int64(len(members))).JSON(c, http.StatusOK)
}

type addMemberRequest struct {
	AuthSub string `json:"auth_sub" binding:"required"`
	Role    string `json:"role" binding:"required,oneof=admin member"`
}

// addMember godoc
// @Summary      Add a member
// @Description  Directly adds an existing user by auth_sub. Admin/owner only.
// @Tags         organization
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string             true  "Organization ID"
// @Param        body            body      addMemberRequest   true  "Member to add"
// @Success      201             {object}  response.Payload{data=membershipRecord}
// @Failure      422             {object}  response.Payload  "validation failed"
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/members [post]
func (h *handler) addMember(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	var req addMemberRequest
	if !request.Bind(c, &req) {
		return
	}

	m, err := h.svc.addMember(c.Request.Context(), ws.ID, req.AuthSub, req.Role)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(m).JSON(c, http.StatusCreated)
}

// removeMember godoc
// @Summary      Remove a member
// @Description  Admin/owner only. Cannot remove yourself or the organization owner.
// @Tags         organization
// @Security     BearerAuth
// @Param        organizationID  path  string  true  "Organization ID"
// @Param        authSub         path  string  true  "Member's auth subject"
// @Success      204             "no content"
// @Failure      400             {object}  response.Payload  "cannot remove yourself"
// @Failure      403             {object}  response.Payload  "cannot remove the owner, or admin role required"
// @Failure      404             {object}  response.Payload  "member not found"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/members/{authSub} [delete]
func (h *handler) removeMember(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	subject := reqctx.Subject(c)
	// Semantic audit label — set before the self/owner guards so a rejected
	// removal attempt is also recorded under this action, not a bare DELETE.
	c.Set("audit.action", "member.removed")

	authSub := c.Param("authSub")
	if authSub == subject {
		response.Error("SELF_REMOVAL", "cannot remove yourself").JSON(c, http.StatusBadRequest)
		return
	}
	if authSub == ws.OwnerID {
		response.Error("CANNOT_REMOVE_OWNER", "cannot remove the organization owner").JSON(c, http.StatusForbidden)
		return
	}

	if err := h.svc.removeMember(c.Request.Context(), ws.ID, authSub); err != nil {
		response.FromError(c, err)
		return
	}
	h.invalidateRole(c, ws.ID, authSub)
	c.Status(http.StatusNoContent)
}

type updateMemberRoleRequest struct {
	Role string `json:"role" binding:"required,oneof=admin member"`
}

// updateMemberRole godoc
// @Summary      Change a member's role
// @Description  Admin/owner only. Cannot change the organization owner's role.
// @Tags         organization
// @Accept       json
// @Security     BearerAuth
// @Param        organizationID  path  string                   true  "Organization ID"
// @Param        authSub         path  string                   true  "Member's auth subject"
// @Param        body            body  updateMemberRoleRequest  true  "New role"
// @Success      204             "no content"
// @Failure      422             {object}  response.Payload  "validation failed"
// @Failure      403             {object}  response.Payload  "cannot change the owner's role, or admin role required"
// @Failure      404             {object}  response.Payload  "member not found"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/members/{authSub}/role [patch]
func (h *handler) updateMemberRole(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	var req updateMemberRoleRequest
	if !request.Bind(c, &req) {
		return
	}

	authSub := c.Param("authSub")
	if authSub == ws.OwnerID {
		response.Error("CANNOT_DEMOTE_OWNER", "cannot change the owner's role").JSON(c, http.StatusForbidden)
		return
	}
	if err := h.svc.updateMemberRole(c.Request.Context(), ws.ID, authSub, req.Role); err != nil {
		response.FromError(c, err)
		return
	}
	h.invalidateRole(c, ws.ID, authSub)
	c.Status(http.StatusNoContent)
}

// leaveOrganization godoc
// @Summary      Leave an organization
// @Description  Removes the caller's own membership.
// @Tags         organization
// @Security     BearerAuth
// @Param        organizationID  path  string  true  "Organization ID"
// @Success      204             "no content"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/leave [delete]
func (h *handler) leaveOrganization(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	subject := reqctx.Subject(c)

	if err := h.svc.leaveOrganization(c.Request.Context(), ws.ID, subject); err != nil {
		response.FromError(c, err)
		return
	}
	h.invalidateRole(c, ws.ID, subject)
	c.Status(http.StatusNoContent)
}

type transferOwnershipRequest struct {
	AuthSub string `json:"auth_sub" binding:"required"`
}

// transferOwnership godoc
// @Summary      Transfer organization ownership
// @Description  Owner only. Requires step-up MFA (aal2) if the caller has MFA enabled.
// @Tags         organization
// @Accept       json
// @Security     BearerAuth
// @Param        organizationID  path  string                    true  "Organization ID"
// @Param        body            body  transferOwnershipRequest  true  "New owner"
// @Success      204             "no content"
// @Failure      422             {object}  response.Payload  "validation failed"
// @Failure      403             {object}  response.Payload  "owner role or MFA step-up required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/transfer [post]
func (h *handler) transferOwnership(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	subject := reqctx.Subject(c)

	var req transferOwnershipRequest
	if !request.Bind(c, &req) {
		return
	}

	if err := h.svc.transferOwnership(c.Request.Context(), ws.ID, subject, req.AuthSub); err != nil {
		response.FromError(c, err)
		return
	}
	h.invalidateOrg(c, ws.ID)
	h.invalidateRole(c, ws.ID, subject)
	h.invalidateRole(c, ws.ID, req.AuthSub)
	c.Status(http.StatusNoContent)
}

// importRowRaw is bound loosely (no binding tags) so a malformed row never
// aborts the whole request — every row is validated manually in the loop
// below instead, which is what lets valid rows still import alongside bad
// ones (this is what enables the dry-run preview).
type importRowRaw struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type importMembersRequest struct {
	Rows   []importRowRaw `json:"rows" binding:"required,max=200"`
	DryRun bool           `json:"dry_run"`
}

type importRowResult struct {
	Index  int    `json:"index"`
	Email  string `json:"email"`
	Reason string `json:"reason"`
}

var importEmailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// validateImportRows checks each row independently — format, role, and
// duplicate emails within the same batch — without touching the database.
// Shared by the dry-run preview and the commit step so both see the exact
// same set of valid rows. validIdx[i] is valid[i]'s position in the original
// rows slice, so the commit loop can report failures against the index the
// caller actually submitted rather than the position in this filtered subset.
func validateImportRows(rows []importRowRaw) (valid []importRowRaw, validIdx []int, errs []importRowResult) {
	seen := make(map[string]bool, len(rows))
	for i, row := range rows {
		email := strings.ToLower(strings.TrimSpace(row.Email))
		switch {
		case email == "" || !importEmailPattern.MatchString(email):
			errs = append(errs, importRowResult{Index: i, Email: row.Email, Reason: "invalid email"})
		case row.Role != contracts.RoleAdmin && row.Role != contracts.RoleMember:
			errs = append(errs, importRowResult{Index: i, Email: email, Reason: fmt.Sprintf("unknown role %q", row.Role)})
		case seen[email]:
			errs = append(errs, importRowResult{Index: i, Email: email, Reason: "duplicate row"})
		default:
			seen[email] = true
			valid = append(valid, importRowRaw{Email: email, Role: row.Role})
			validIdx = append(validIdx, i)
		}
	}
	return valid, validIdx, errs
}

// importMembers bulk-invites by email (columns: email, role — not the
// auth_sub direct-add this replaced), with a dry-run preview before commit:
// rows that fail validation are reported without blocking the
// rows that pass. Reuses createInvitation per row, so a re-imported email
// resends rather than erroring (insertInvitation's own ON CONFLICT), and
// each invite still gets its own MemberInvited event/email.
//
// @Summary      Bulk-import members by email
// @Description  Bulk-invites by email + role, up to 200 rows per request. With dry_run=true, validates and returns counts/errors without inviting anyone; otherwise commits and returns imported/failed counts. Admin/owner only.
// @Tags         organization
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string                                                                true  "Organization ID"
// @Param        body            body      importMembersRequest                                                  true  "Rows to import"
// @Success      200             {object}  response.Payload{data=object{imported=integer,failed=[]importRowResult}}  "commit result (dry_run=false)"
// @Failure      422             {object}  response.Payload  "validation failed"
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/members/import [post]
func (h *handler) importMembers(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	var req importMembersRequest
	if !request.Bind(c, &req) {
		return
	}

	valid, validIdx, errs := validateImportRows(req.Rows)

	if req.DryRun {
		response.Success(gin.H{"valid_count": len(valid), "errors": errs}).JSON(c, http.StatusOK)
		return
	}

	imported := 0
	failed := errs
	for i, row := range valid {
		if _, err := h.svc.createInvitation(c.Request.Context(), ws.ID, row.Email, row.Role, reqctx.Subject(c)); err != nil {
			reason := "failed to invite"
			switch {
			case errors.Is(err, ErrPlanLimitReached):
				reason = "plan member limit reached"
			case errors.Is(err, ErrInviteeAlreadyMember):
				reason = "already a member"
			}
			failed = append(failed, importRowResult{Index: validIdx[i], Email: row.Email, Reason: reason})
		} else {
			imported++
		}
	}

	response.Success(gin.H{"imported": imported, "failed": failed}).JSON(c, http.StatusOK)
}
