package reference

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// Golden-response check for the item-13 apperr/response.FromError migration:
// the JSON shape/code/message for an unwired catalog reader must stay
// byte-identical to what the pre-migration handler produced by hand.
func TestListPlans_CatalogNotWired_GoldenResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	h := &handler{svc: &service{}} // catalog left nil
	e.GET("/references/plans", h.listPlans)

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/references/plans", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", w.Code)
	}
	const want = `{"status":{"request_id":"","error":true,"code":"PLANS_FETCH_FAILED","message":"failed to list plans"}}`
	if got := w.Body.String(); got != want {
		t.Errorf("body mismatch:\n want %s\n got  %s", want, got)
	}
}
