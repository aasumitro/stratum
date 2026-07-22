package billing

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/httpserver/reqctx"
	"github.com/aasumitro/stratum/internal/platform/httpserver/request"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

// --- coupons ---

type redeemCouponRequest struct {
	Code string `json:"code" binding:"required"`
}

// redeemCoupon godoc
// @Summary      Redeem a coupon
// @Description  Owner only.
// @Tags         billing
// @Accept       json
// @Security     BearerAuth
// @Param        organizationID  path  string               true  "Organization ID"
// @Param        body            body  redeemCouponRequest  true  "Coupon code"
// @Success      204             "no content"
// @Failure      422             {object}  response.Payload  "validation failed or invalid/ineligible coupon"
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/coupons/redeem [post]
func (h *handler) redeemCoupon(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	var req redeemCouponRequest
	if !request.Bind(c, &req) {
		return
	}

	if err := h.svc.redeemCoupon(
		c.Request.Context(), ws.ID,
		reqctx.Subject(c), req.Code,
	); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// listEligibleCoupons godoc
// @Summary      List eligible coupons
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=[]couponRecord}
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/coupons [get]
func (h *handler) listEligibleCoupons(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	coupons, err := h.svc.listEligibleCoupons(
		c.Request.Context(), ws.ID, reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(coupons).JSON(c, http.StatusOK)
}

// listEligibleCouponsForNewOrg godoc
// @Summary      List coupons eligible for a not-yet-created organization
// @Description  Same eligibility rules as the per-organization list, minus org-targeted coupons (there is no organization yet) — for the "create organization" cart.
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.Payload{data=[]couponRecord}
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /billing/coupons/eligible [get]
func (h *handler) listEligibleCouponsForNewOrg(c *gin.Context) {
	coupons, err := h.svc.listEligibleCoupons(
		c.Request.Context(), "", reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(coupons).JSON(c, http.StatusOK)
}
