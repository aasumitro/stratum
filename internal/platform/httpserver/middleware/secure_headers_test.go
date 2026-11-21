package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
)

func TestSecureHeaders_HSTSOnlyWhenAsked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for hsts, want := range map[bool]string{
		false: "",
		true:  "max-age=31536000",
	} {
		e := gin.New()
		e.Use(middleware.SecureHeaders(hsts))
		e.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

		w := httptest.NewRecorder()
		e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

		if got := w.Header().Get("Strict-Transport-Security"); got != want {
			t.Errorf("hsts=%v: Strict-Transport-Security = %q, want %q", hsts, got, want)
		}
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("hsts=%v: X-Content-Type-Options = %q", hsts, got)
		}
	}
}
