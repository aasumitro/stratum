package reference

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/geoip"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

// swag can only resolve a cross-package type referenced in a @Success/@Failure
// annotation if the package is imported in the same file as the annotation —
// contracts is otherwise unused here (listPlans/listFeatures/listAddons infer
// their types from service.go).
var (
	_ contracts.PlanInfo
	_ contracts.FeatureInfo
	_ contracts.AddonInfo
)

type handler struct {
	svc             *service
	countryResolver *geoip.Resolver
}

// resolveCountryCode resolves the caller's trusted billing country the same
// way organization.handler.createOrganization does (geoip.Resolver.Resolve)
// — GeoIP on their real IP, with development's debug-header override and
// the shared "US" fallback baked into Resolve itself.
func (h *handler) resolveCountryCode(c *gin.Context) string {
	return h.countryResolver.Resolve(c.Request.Context(), c.ClientIP(), c.GetHeader(geoip.DebugCountryCodeHeader))
}

// listCountries godoc
// @Summary      List countries
// @Description  Returns the static reference list of countries.
// @Tags         reference
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.Payload{data=[]countryRecord}
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /references/countries [get]
func (h *handler) listCountries(c *gin.Context) {
	countries, err := h.svc.listCountries(c.Request.Context())
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.List(countries, int64(len(countries))).JSON(c, http.StatusOK)
}

// listCurrencies godoc
// @Summary      List currencies
// @Description  Returns the static reference list of currencies.
// @Tags         reference
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.Payload{data=[]currencyRecord}
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /references/currencies [get]
func (h *handler) listCurrencies(c *gin.Context) {
	currencies, err := h.svc.listCurrencies(c.Request.Context())
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.List(currencies, int64(len(currencies))).JSON(c, http.StatusOK)
}

// listPlans godoc
// @Summary      List billing plans
// @Description  Returns the billing plan catalog (pricing, limits, features); delegated to the billing module's catalog reader. Prices are scoped server-side to the currency the caller's real (GeoIP-resolved) country is billed in.
// @Tags         reference
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.Payload{data=[]contracts.PlanInfo}
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /references/plans [get]
func (h *handler) listPlans(c *gin.Context) {
	plans, err := h.svc.listPlans(c.Request.Context(), h.resolveCountryCode(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.List(plans, int64(len(plans))).JSON(c, http.StatusOK)
}

// listFeatures godoc
// @Summary      List billing features
// @Description  Returns the billing feature catalog; delegated to the billing module's catalog reader.
// @Tags         reference
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.Payload{data=[]contracts.FeatureInfo}
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /references/features [get]
func (h *handler) listFeatures(c *gin.Context) {
	features, err := h.svc.listFeatures(c.Request.Context())
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.List(features, int64(len(features))).JSON(c, http.StatusOK)
}

// listAddons godoc
// @Summary      List billing addons
// @Description  Returns the billing addon catalog; delegated to the billing module's catalog reader. Prices are scoped server-side to the currency the caller's real (GeoIP-resolved) country is billed in.
// @Tags         reference
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.Payload{data=[]contracts.AddonInfo}
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /references/addons [get]
func (h *handler) listAddons(c *gin.Context) {
	addons, err := h.svc.listAddons(c.Request.Context(), h.resolveCountryCode(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.List(addons, int64(len(addons))).JSON(c, http.StatusOK)
}
