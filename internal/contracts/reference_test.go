package contracts

import "testing"

func TestResolveCurrency(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ID", "IDR"},
		{"US", "USD"},
		{"GB", "USD"},
		{"", "USD"},
		{"id", "USD"}, // case-sensitive
	}
	for _, c := range cases {
		if got := ResolveCurrency(c.in); got != c.want {
			t.Errorf("ResolveCurrency(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
