package middleware

import "testing"

func TestIPAllowed(t *testing.T) {
	cases := []struct {
		name    string
		client  string
		allowed []string
		want    bool
	}{
		{"exact ip match", "203.0.113.5", []string{"203.0.113.5"}, true},
		{"cidr match", "10.1.2.3", []string{"10.1.0.0/16"}, true},
		{"cidr no match", "10.2.2.3", []string{"10.1.0.0/16"}, false},
		{"not in list", "8.8.8.8", []string{"203.0.113.5", "10.0.0.0/8"}, false},
		{"empty list denies", "8.8.8.8", nil, false},
		{"unparseable client denies", "not-an-ip", []string{"0.0.0.0/0"}, false},
		{"ipv6 cidr match", "2001:db8::1", []string{"2001:db8::/32"}, true},
		{"malformed entry skipped, later entry matches", "203.0.113.5", []string{"garbage", "203.0.113.5"}, true},
		// IPv4-mapped IPv6 form (common behind a dual-stack listener) must
		// still match a bare IPv4 allowlist entry — raw string equality
		// used to miss this.
		{"ipv4-mapped ipv6 matches bare ipv4 entry", "::ffff:127.0.0.1", []string{"127.0.0.1"}, true},
		// IPv6 loopback is a genuinely different address from IPv4 loopback
		// — normalizing IPv4-mapped forms must not blur this distinction.
		{"ipv6 loopback does not match ipv4 loopback entry", "::1", []string{"127.0.0.1"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ipAllowed(c.client, c.allowed); got != c.want {
				t.Errorf("ipAllowed(%q, %v) = %v, want %v", c.client, c.allowed, got, c.want)
			}
		})
	}
}
