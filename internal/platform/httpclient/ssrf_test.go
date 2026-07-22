package httpclient

import (
	"net"
	"testing"
)

func TestBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1",       // loopback
		"10.0.0.5",        // RFC1918
		"172.16.0.5",      // RFC1918
		"192.168.1.5",     // RFC1918
		"169.254.169.254", // cloud metadata (link-local)
		"0.0.0.0",         // unspecified
		"::1",             // IPv6 loopback
		"fc00::1",         // IPv6 ULA
	}
	for _, s := range blocked {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("test bug: %q did not parse", s)
		}
		if !blockedIP(ip) {
			t.Errorf("blockedIP(%s) = false, want true", s)
		}
	}

	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34"}
	for _, s := range allowed {
		ip := net.ParseIP(s)
		if blockedIP(ip) {
			t.Errorf("blockedIP(%s) = true, want false", s)
		}
	}
}

func TestValidateOutboundURL(t *testing.T) {
	ctx := t.Context()

	if err := ValidateOutboundURL(ctx, "http://example.com/hook"); err == nil {
		t.Error("expected error for non-https scheme, got nil")
	}
	if err := ValidateOutboundURL(ctx, "https://localhost/hook"); err == nil {
		t.Error("expected error for loopback host, got nil")
	}
	if err := ValidateOutboundURL(ctx, "not-a-url"); err == nil {
		t.Error("expected error for unparseable/hostless url, got nil")
	}
}
