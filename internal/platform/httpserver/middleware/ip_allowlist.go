package middleware

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
)

// NewIPAllowlistMiddleware restricts access to the route based on the
// caller's IP, resolved via gin's Context.ClientIP(). ClientIP() is only
// safe to trust once (*gin.Engine).SetTrustedProxies is configured to
// match this service's actual deployment topology — see httpserver.New,
// which wires it from config.TrustedProxies. Without that configuration,
// gin still refuses to honor any forwarded-for header (ClientIP() falls
// back to the raw TCP peer address), so this fails closed rather than
// trusting a forgeable header by accident; with it configured correctly,
// this correctly sees the real caller's IP through a load balancer/ingress
// instead of always rejecting (or, worse, always seeing the proxy's own
// IP and never matching a real allowlist entry).
//
// An empty cidrs list allows all traffic — config validation
// (config.RequireWebhookSecretsOutsideDev) refuses to boot outside
// development with this route's allowlist unset, but this also logs a
// warning so an empty list is never silently invisible in this file alone.
func NewIPAllowlistMiddleware(cidrs []string) (gin.HandlerFunc, error) {
	if len(cidrs) == 0 {
		slog.Warn("ip allowlist: empty CIDR list, allowing all traffic — must not run outside development")
		return func(c *gin.Context) { c.Next() }, nil
	}

	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("ip allowlist: invalid CIDR %q: %w", cidr, err)
		}
		nets = append(nets, n)
	}

	return func(c *gin.Context) {
		clientIP := c.ClientIP()
		ip := net.ParseIP(clientIP)
		for _, n := range nets {
			if ip != nil && n.Contains(ip) {
				c.Next()
				return
			}
		}
		slog.WarnContext(c.Request.Context(), "ip allowlist: rejected request", "client_ip", clientIP)
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "source not allowed"})
	}, nil
}
