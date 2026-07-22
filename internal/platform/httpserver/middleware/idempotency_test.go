package middleware

// Internal (package middleware) to test idempotencyCacheKey directly,
// mirroring ratelimiter_test.go: this codebase has no Redis test harness
// anywhere, so these tests cover the cache-key scoping logic — the actual
// fix for item 5's cross-user collision — not the caching mechanics.

import "testing"

func TestIdempotencyCacheKey_ScopesByCallerAndRoute(t *testing.T) {
	base := idempotencyCacheKey("sub_a", "POST", "/billing/invoices/1/pay", "key-1")

	cases := map[string]string{
		"different subject": idempotencyCacheKey("sub_b", "POST", "/billing/invoices/1/pay", "key-1"),
		"different method":  idempotencyCacheKey("sub_a", "GET", "/billing/invoices/1/pay", "key-1"),
		"different path":    idempotencyCacheKey("sub_a", "POST", "/billing/invoices/2/pay", "key-1"),
		"different raw key": idempotencyCacheKey("sub_a", "POST", "/billing/invoices/1/pay", "key-2"),
	}
	for name, other := range cases {
		if other == base {
			t.Errorf("%s: expected a distinct cache key, got the same as base (%q) — this is exactly the cross-user collision item 5 fixes", name, base)
		}
	}

	if got := idempotencyCacheKey("sub_a", "POST", "/billing/invoices/1/pay", "key-1"); got != base {
		t.Errorf("same inputs should produce the same key: got %q, want %q", got, base)
	}
}
