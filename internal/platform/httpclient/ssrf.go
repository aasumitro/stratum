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
					return nil, fmt.Errorf("httpclient.SSRFSafeClient: split host/port: %w", err)
				}
				ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
				if err != nil {
					return nil, fmt.Errorf("httpclient.SSRFSafeClient: resolve %q: %w", host, err)
				}
				return dialValidated(ctx, dialer.DialContext, network, port, ips)
			},
		},
	}
}

// dialValidated rejects the whole candidate set if any resolved address is
// non-routable (an attacker with one public and one private DNS answer
// can't bypass the check by getting lucky on fallback order), then dials
// the validated IPs directly in order — not the hostname again, so a
// second DNS lookup between here and the actual connect can't swap in a
// different answer — falling back through the list on a dial failure so a
// dead first address (e.g. a stale/unreachable AAAA record) doesn't fail
// the whole request when a later resolved address would have worked.
// Extracted from SSRFSafeClient's DialContext closure so both the
// empty-slice guard and the fallback behavior are unit-testable with a
// fake dial func, without a real network dial.
func dialValidated(
	ctx context.Context,
	dial func(ctx context.Context, network, addr string) (net.Conn, error),
	network, port string,
	ips []net.IP,
) (net.Conn, error) {
	if len(ips) == 0 {
		// LookupIP can return a nil error with zero addresses under some
		// resolver configurations — indexing ips[0] unchecked would panic
		// instead of failing the dial.
		return nil, fmt.Errorf("httpclient: no IP addresses resolved")
	}
	for _, ip := range ips {
		if blockedIP(ip) {
			return nil, fmt.Errorf("httpclient: refusing to dial non-routable address %s", ip)
		}
	}
	var dialErr error
	for _, ip := range ips {
		conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		dialErr = err
	}
	return nil, dialErr
}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}
