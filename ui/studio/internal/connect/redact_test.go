package connect

import "testing"

func TestRedactDSN(t *testing.T) {
	cases := map[string]string{
		"cannot parse `postgres://admin:s3cret@db.internal:5432/app`: invalid": "cannot parse `postgres://admin:***@db.internal:5432/app`: invalid",
		"dial amqp://guest:guest@localhost:5672/: connection refused":          "dial amqp://guest:***@localhost:5672/: connection refused",
		"parse \"redis://user:p@ss\": net/url: invalid control character":      "parse \"redis://user:***@ss\": net/url: invalid control character",
		"connection refused": "connection refused",
		"no credentials in postgres://db.internal:5432/app": "no credentials in postgres://db.internal:5432/app",
	}
	for in, want := range cases {
		if got := RedactDSN(in); got != want {
			t.Errorf("RedactDSN(%q) = %q, want %q", in, got, want)
		}
	}
}
