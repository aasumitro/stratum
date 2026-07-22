package httpclient

import (
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
