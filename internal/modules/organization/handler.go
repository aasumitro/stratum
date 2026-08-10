package organization

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/geoip"
)

const (
	maxLogoSize       = 2 << 20 // 2 MB
	settingAllowedIPs = "allowed_ips"
)

type handler struct {
	svc             *service
	pool            *pgxpool.Pool
	cacheInval      contracts.OrganizationCacheInvalidator
	countryResolver *geoip.Resolver
}

func (h *handler) invalidateOrg(c *gin.Context, organizationID string) {
	if h.cacheInval != nil {
		h.cacheInval.InvalidateOrganization(c.Request.Context(), organizationID)
	}
}

func (h *handler) invalidateRole(c *gin.Context, organizationID, authSub string) {
	if h.cacheInval != nil {
		h.cacheInval.InvalidateMemberRole(c.Request.Context(), organizationID, authSub)
	}
}
