package notification

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestQueryIntClamped regression-tests that listNotifications' limit can't
// be pushed past max (e.g. ?limit=100000000 forcing a huge scan/result
// materialization) — see queryIntClamped.
func TestQueryIntClamped(t *testing.T) {
	newCtx := func(query string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/?"+query, nil)
		return c
	}

	cases := []struct {
		name          string
		query         string
		fallback, max int
		want          int
	}{
		{"within bound", "limit=50", 20, 100, 50},
		{"exceeds max clamps to max", "limit=100000000", 20, 100, 100},
		{"missing uses fallback", "", 20, 100, 20},
		{"zero uses fallback", "limit=0", 20, 100, 20},
		{"negative uses fallback", "limit=-5", 20, 100, 20},
		{"exactly max is unclamped", "limit=100", 20, 100, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCtx(tc.query)
			if got := queryIntClamped(c, "limit", tc.fallback, tc.max); got != tc.want {
				t.Errorf("queryIntClamped(%q) = %d, want %d", tc.query, got, tc.want)
			}
		})
	}
}
