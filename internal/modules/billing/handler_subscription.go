package billing

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/audit"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/httpserver/reqctx"
	"github.com/aasumitro/stratum/internal/platform/httpserver/request"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

// --- subscription routes (organization-scoped) ---

// subscriptionWithCoupon extends subscriptionRecord's JSON shape with the
// subscription's currently-active coupon code, if any — kept as a response
// wrapper rather than a field on subscriptionRecord itself so every other
// call site returning that type is unaffected.
type subscriptionWithCoupon struct {
	subscriptionRecord
	ActiveCoupon *string `json:"active_coupon,omitempty"`
}

// getSubscription godoc
// @Summary      Get the organization's subscription
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=subscriptionWithCoupon}
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing [get]
func (h *handler) getSubscription(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	sub, err := h.svc.getSubscription(
		c.Request.Context(), "organization", ws.ID)
	if err != nil {
		response.FromError(c, err)
		return
	}

	resp := subscriptionWithCoupon{subscriptionRecord: *sub}
	if redemption, err := h.svc.findActiveCouponRedemption(
		c.Request.Context(), h.svc.querier(c.Request.Context()), sub.ID,
	); err == nil {
		resp.ActiveCoupon = &redemption.CouponCode
	}
	response.Success(resp).JSON(c, http.StatusOK)
}

type changePlanRequest struct {
	// Plan is validated dynamically against billing.plans via the catalog
	// lookup in changePlan — no oneof here: a static enum would reject any plan an
	// operator adds through Studio's catalog composition UI that isn't one
	// of the 3 originally-seeded slugs.
	Plan  string `json:"plan"  binding:"required"`
	Cycle string `json:"cycle" binding:"required,oneof=monthly yearly"`
}

// changePlan godoc
// @Summary      Change the subscription plan
// @Description  Owner only. Requires step-up MFA (aal2) if the caller has MFA enabled.
// @Tags         billing
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string             true  "Organization ID"
// @Param        body            body      changePlanRequest  true  "New plan and cycle"
// @Success      200             {object}  response.Payload{data=subscriptionRecord}
// @Failure      422             {object}  response.Payload  "validation failed"
// @Failure      403             {object}  response.Payload  "owner role or MFA step-up required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/plan [patch]
func (h *handler) changePlan(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	if h.svc != nil {
		if before, err := h.svc.getSubscription(
			c.Request.Context(), "organization", ws.ID,
		); err == nil {
			audit.SetBefore(c, map[string]any{"plan": before.Plan, "cycle": before.Cycle, "status": before.Status})
		}
	}

	var req changePlanRequest
	if !request.Bind(c, &req) {
		return
	}

	sub, err := h.svc.changePlan(
		c.Request.Context(), "organization",
		ws.ID, req.Plan, req.Cycle, reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	audit.SetAfter(c, map[string]any{"plan": sub.Plan, "cycle": sub.Cycle, "status": sub.Status})
	response.Success(sub).JSON(c, http.StatusOK)
}

type downgradeSubscriptionRequest struct {
	Plan                    string   `json:"plan" binding:"required"`
	Cycle                   string   `json:"cycle" binding:"required,oneof=monthly yearly"`
	PreferredMemberAuthSubs []string `json:"preferred_member_auth_subs"`
	PreferredFileIDs        []string `json:"preferred_file_ids"`
}

// downgradeSubscriptionResponse carries the OverageResolution alongside the
// updated subscription so the frontend's success screen can show exactly
// what was removed and whether each item was owner-selected or
// auto-selected, instead of only recording it in the audit trail.
type downgradeSubscriptionResponse struct {
	Subscription *subscriptionRecord         `json:"subscription"`
	Overage      contracts.OverageResolution `json:"overage"`
}

// downgradeSubscription godoc
// @Summary      Downgrade the subscription plan and resolve overage
// @Description  Owner only. Requires step-up MFA (aal2) if the caller has MFA enabled.
// @Tags         billing
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string                        true  "Organization ID"
// @Param        body            body      downgradeSubscriptionRequest  true  "Downgrade details"
// @Success      200             {object}  response.Payload{data=downgradeSubscriptionResponse}
// @Failure      422             {object}  response.Payload  "validation failed"
// @Failure      403             {object}  response.Payload  "owner role or MFA step-up required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/downgrade [post]
func (h *handler) downgradeSubscription(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	sub, err := h.svc.getSubscription(c.Request.Context(), "organization", ws.ID)
	if err == nil {
		audit.SetBefore(c, map[string]any{"current_plan": sub.Plan, "current_cycle": sub.Cycle})
	}

	var req downgradeSubscriptionRequest
	if !request.Bind(c, &req) {
		return
	}

	audit.SetAfter(c, map[string]any{"target_plan": req.Plan, "target_cycle": req.Cycle, "preferred_member_auth_subs": req.PreferredMemberAuthSubs, "preferred_file_ids": req.PreferredFileIDs})

	updated, overage, err := h.svc.downgradeSubscription(c.Request.Context(), "organization", ws.ID, req.Plan, req.Cycle, reqctx.Subject(c), req.PreferredMemberAuthSubs, req.PreferredFileIDs)
	if err != nil {
		response.FromError(c, err)
		return
	}
	audit.SetAfter(c, map[string]any{"plan": updated.Plan, "cycle": updated.Cycle, "status": updated.Status})
	response.Success(downgradeSubscriptionResponse{Subscription: updated, Overage: overage}).JSON(c, http.StatusOK)
}

// cancelSubscription godoc
// @Summary      Cancel the subscription
// @Description  Owner only.
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=subscriptionRecord}
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/cancel [post]
func (h *handler) cancelSubscription(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	sub, err := h.svc.cancelSubscription(
		c.Request.Context(), "organization",
		ws.ID, reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(sub).JSON(c, http.StatusOK)
}

type extendSubscriptionRequest struct {
	Months int `json:"months" binding:"required,min=1,max=24"`
}

// extendSubscription godoc
// @Summary      Extend the subscription
// @Description  Owner only. Requires step-up MFA (aal2) if the caller has MFA enabled.
// @Tags         billing
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string                      true  "Organization ID"
// @Param        body            body      extendSubscriptionRequest  true  "Months to extend by"
// @Success      200             {object}  response.Payload{data=invoiceRecord}
// @Failure      422             {object}  response.Payload  "validation failed"
// @Failure      403             {object}  response.Payload  "owner role or MFA step-up required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/extend [post]
func (h *handler) extendSubscription(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	var req extendSubscriptionRequest
	if !request.Bind(c, &req) {
		return
	}

	inv, err := h.svc.extendSubscription(
		c.Request.Context(), "organization",
		ws.ID, req.Months, reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(inv).JSON(c, http.StatusOK)
}

// activateTrialNow godoc
// @Summary      Activate a trial immediately
// @Description  Owner only. Converts an active trial to a paid subscription before its scheduled end. Requires step-up MFA (aal2) if the caller has MFA enabled.
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=invoiceRecord}
// @Failure      403             {object}  response.Payload  "owner role or MFA step-up required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/activate [post]
func (h *handler) activateTrialNow(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	inv, err := h.svc.activateTrialNow(
		c.Request.Context(), "organization",
		ws.ID, reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(inv).JSON(c, http.StatusOK)
}

// resumeSubscription godoc
// @Summary      Resume a cancelled subscription
// @Description  Owner only.
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=subscriptionRecord}
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/resume [post]
func (h *handler) resumeSubscription(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	sub, err := h.svc.resumeSubscription(
		c.Request.Context(), "organization",
		ws.ID, reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(sub).JSON(c, http.StatusOK)
}
