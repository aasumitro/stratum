package httpclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSSRFSafeClient_RefusesLoopbackDial is the security-critical assertion:
// even handed a live URL, the client must refuse to connect to a loopback
// address at dial time (the actual SSRF hole is closed in DialContext).
func TestSSRFSafeClient_RefusesLoopbackDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// srv.URL is http://127.0.0.1:<port> — a real, reachable server.
	if _, err := SSRFSafeClient().Get(srv.URL); err == nil {
		t.Fatal("SSRFSafeClient connected to a loopback address; expected refusal")
	} else if !strings.Contains(err.Error(), "non-routable") {
		t.Errorf("want a non-routable dial refusal, got: %v", err)
	}
}

// TestSSRFSafeClient_HasTimeout regression-tests that the client sets its
// own request timeout rather than relying entirely on every caller to
// supply a context deadline — a future caller that forgets one would
// otherwise be able to hang indefinitely on a tenant-supplied URL.
func TestSSRFSafeClient_HasTimeout(t *testing.T) {
	if got := SSRFSafeClient().Timeout; got <= 0 {
		t.Errorf("SSRFSafeClient().Timeout = %v, want a positive timeout", got)
	}
}

func TestValidateOutboundURL_PrivateIPLiteral_Blocked(t *testing.T) {
	// An RFC1918 literal resolves to itself — no DNS needed, deterministic.
	if err := ValidateOutboundURL(t.Context(), "https://10.0.0.1/hook"); err == nil {
		t.Error("expected private-IP literal to be rejected as non-routable")
	}
}

func TestValidateOutboundURL_UnresolvableHost_Errors(t *testing.T) {
	// .invalid is reserved (RFC 6761) and never resolves.
	if err := ValidateOutboundURL(t.Context(), "https://no-such-host.invalid/hook"); err == nil {
		t.Error("expected a resolve error for an unresolvable host")
	}
}

func TestValidateOutboundURL_ControlCharInURL_ParseError(t *testing.T) {
	if err := ValidateOutboundURL(t.Context(), "https://exa\x7fmple.com"); err == nil {
		t.Error("expected a parse error for a URL containing a control character")
	}
}

// TestDialValidated_EmptyIPs_ErrorsNotPanics guards against a regression
// where LookupIP returning a nil error alongside zero resolved addresses
// (possible under some resolver configurations) crashed the dial via an
// unchecked ips[0] instead of failing it cleanly.
func TestDialValidated_EmptyIPs_ErrorsNotPanics(t *testing.T) {
	dial := func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("dial must not be called for an empty ips slice")
		return nil, nil
	}
	_, err := dialValidated(t.Context(), dial, "tcp", "443", nil)
	if err == nil {
		t.Error("want an error for zero resolved addresses, got nil")
	}
}

// TestDialValidated_AnyBlockedIP_RejectsWholeSet regression-tests that a
// single non-routable address in the resolved set rejects the whole dial
// — even alongside an otherwise-valid public address — and does so before
// ever attempting to dial, so an attacker can't get a real connection by
// having DNS answer with one public and one private address.
func TestDialValidated_AnyBlockedIP_RejectsWholeSet(t *testing.T) {
	dialed := false
	dial := func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, nil
	}
	ips := []net.IP{net.ParseIP("203.0.113.1"), net.ParseIP("127.0.0.1")} // TEST-NET-3 + loopback
	_, err := dialValidated(t.Context(), dial, "tcp", "443", ips)
	if err == nil || !strings.Contains(err.Error(), "non-routable") {
		t.Errorf("want a non-routable rejection, got: %v", err)
	}
	if dialed {
		t.Error("dial must not run once any resolved address is blocked")
	}
}

// TestDialValidated_FallsBackOnDialFailure regression-tests the dual-stack
// fallback: a dead first address (e.g. a stale/unreachable AAAA record)
// must not fail the whole request when a later resolved address is
// reachable.
func TestDialValidated_FallsBackOnDialFailure(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	var attempts []string
	dial := func(_ context.Context, _ string, addr string) (net.Conn, error) {
		attempts = append(attempts, addr)
		if addr == "203.0.113.1:443" {
			return nil, errors.New("connection refused")
		}
		return client, nil
	}
	ips := []net.IP{net.ParseIP("203.0.113.1"), net.ParseIP("203.0.113.2")} // both TEST-NET-3, neither blocked
	conn, err := dialValidated(t.Context(), dial, "tcp", "443", ips)
	if err != nil {
		t.Fatalf("want the second address to succeed, got error: %v", err)
	}
	if conn != client {
		t.Error("want the successful dial's connection returned")
	}
	if len(attempts) != 2 {
		t.Errorf("want both addresses attempted in order, got %v", attempts)
	}
}
