package reference_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aasumitro/stratum/internal/modules/reference"
	"github.com/aasumitro/stratum/internal/platform/geoip"
)

// testGeoIPResolverForReference builds a resolver against the same MaxMind
// test fixture internal/platform/geoip's own unit tests use — 81.2.69.142
// resolves to GB, which this catalog has no IDR entry for, so it must fall
// back to USD (contracts.ResolveCurrency's own default), never IDR.
func testGeoIPResolverForReference(t *testing.T) *geoip.Resolver {
	t.Helper()
	r, err := geoip.New("../../platform/geoip/testdata/GeoIP2-Country-Test.mmdb", false)
	if err != nil {
		t.Fatalf("open test geoip db: %v", err)
	}
	return r
}

// TestIntegration_ListPlans_IgnoresClientSuppliedCountryCode is the
// end-to-end proof for this fix: a caller cannot pick which currency they
// see (and, transitively, would be billed in, had this been the org-
// creation path) by passing ?country_code= — only the resolved GeoIP result
// on the real request IP ever scopes the response.
func TestIntegration_ListPlans_IgnoresClientSuppliedCountryCode(t *testing.T) {
	resolver := testGeoIPResolverForReference(t)
	e := reference.NewModuleEngineWithCountryResolver(testPoolReference(t), resolver)

	// ?country_code=ID tries to smuggle in IDR pricing; 81.2.69.142 (a
	// MaxMind test fixture IP) resolves to GB, which this catalog has no
	// IDR entry for either — the real proof is that the response scopes to
	// USD (the resolved fallback), never IDR, regardless of the query string.
	req := httptest.NewRequest(http.MethodGet, "/api/references/plans?country_code=ID", nil)
	req.RemoteAddr = "81.2.69.142:1234"

	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data, _ := resp["data"].([]any)
	if len(data) == 0 {
		t.Fatal("want at least one plan in response")
	}
	for _, p := range data {
		plan, _ := p.(map[string]any)
		prices, _ := plan["prices"].(map[string]any)
		if len(prices) != 1 {
			t.Errorf("plan %v: want exactly 1 currency, got %d: %v", plan["id"], len(prices), prices)
		}
		if _, ok := prices["IDR"]; ok {
			t.Errorf("plan %v: query string's country_code=ID must not win — got IDR pricing from a GB-resolved request", plan["id"])
		}
	}
}
