package reqctx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/httpserver/reqctx"
)

func init() { gin.SetMode(gin.TestMode) }

func TestSubject_ReturnsClaimSubject(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Set("auth.claims", middleware.Claims{Subject: "auth0|abc123"})

	if got := reqctx.Subject(c); got != "auth0|abc123" {
		t.Errorf("Subject = %q, want auth0|abc123", got)
	}
}

func TestSubject_EmptyWhenNoClaims(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	// no claims attached (an unauthenticated route)
	if got := reqctx.Subject(c); got != "" {
		t.Errorf("Subject on unauthed context = %q, want empty", got)
	}
}
