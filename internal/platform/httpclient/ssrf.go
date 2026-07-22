// Package httpclient provides hardened HTTP primitives for requests to
// URLs supplied by a tenant rather than our own config — outbound webhooks
// today, any future integration callback later.
package httpclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"time"
)

// ssrfClientTimeout bounds the whole request (dial + headers + body) for
// SSRFSafeClient. The only caller today already wraps each call in its own
// shorter context deadline, so this never fires in practice — it exists so
// a future caller that forgets to set one can't hang indefinitely on a
// tenant-supplied URL.
const ssrfClientTimeout = 30 * time.Second

// ValidateOutboundURL enforces the trust boundary for a tenant-supplied
// destination URL: https only, and every address the hostname resolves to
// must be publicly routable. Call this once, at registration time. It does
// not guarantee delivery-time safety by itself — a hostname's DNS answer
// can change between registration and delivery (DNS rebinding) — so
// SSRFSafeClient re-validates on every dial too.
func ValidateOutboundURL(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("httpclient.ValidateOutboundURL: parse: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("httpclient.ValidateOutboundURL: url must use https")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("httpclient.ValidateOutboundURL: url has no host")
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("httpclient.ValidateOutboundURL: resolve %q: %w", host, err)
	}
	if slices.ContainsFunc(ips, blockedIP) {
		return fmt.Errorf("httpclient.ValidateOutboundURL: %q resolves to a non-routable address", host)
	}
	return nil
}

// SSRFSafeClient returns an *http.Client for requests to tenant-supplied
// URLs. Every dial re-resolves the target host and refuses to connect if
// any candidate address is loopback, private (RFC1918 / IPv6 ULA),
// link-local (including the 169.254.169.254 cloud metadata address),
// unspecified, or multicast — this is what actually closes the SSRF hole,
// since it runs at connect time, not just at ValidateOutboundURL's
// one-time check. Redirects are capped and must stay on https; each
// redirect hop is revalidated automatically because it triggers a fresh
// DialContext call.
func SSRFSafeClient() *http.Client {
	dialer := &net.Dialer{}
	return &http.Client{
		Timeout: ssrfClientTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("httpclient: stopped after 5 redirects")
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("httpclient: redirect to non-https url blocked")
			}
			return nil
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
				if err != nil {
					return nil, err
				}
				for _, ip := range ips {
					if blockedIP(ip) {
						return nil, fmt.Errorf("httpclient: refusing to dial non-routable address %s", ip)
					}
				}
				// Dial the already-validated IP directly rather than the
				// hostname again, so a second DNS lookup between here and
				// the actual connect can't swap in a different answer.
				return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
			},
		},
	}
}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}
