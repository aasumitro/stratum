package organization_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/geoip"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// testGeoIPResolver builds a resolver against the same MaxMind test fixture
// internal/platform/geoip's own unit tests use — 81.2.69.142 resolves to GB,
// 1.1.1.1 has no data in it (a deliberate miss, for the fallback case).
func testGeoIPResolver(t *testing.T) *geoip.Resolver {
	t.Helper()
	r, err := geoip.New("../../platform/geoip/testdata/GeoIP2-Country-Test.mmdb", false)
	if err != nil {
		t.Fatalf("open test geoip db: %v", err)
	}
	return r
}

// TestIntegration_CreateOrganization_IgnoresClientSuppliedCountryCode is the
// end-to-end proof for this fix: a caller cannot pick their own billing
// country/currency by sending country_code in the request body (the field
// doesn't even exist in createOrganizationRequest anymore, so it's silently
// dropped) or by any other client-controlled means — only the resolved
// GeoIP result on the real request IP ever reaches organizations.country_code.
func TestIntegration_CreateOrganization_IgnoresClientSuppliedCountryCode(t *testing.T) {
	pool := testPool(t)
	resolver := testGeoIPResolver(t)
	const slug = "integ-ws-geoip-gb"
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE slug = $1`, slug)
	})

	req := httpserver.JSONTestRequest(http.MethodPost, "/api/organizations",
		`{"slug":"`+slug+`","name":"Integ WS","plan":"solo","cycle":"monthly","country_code":"ID"}`)
	// 81.2.69.142 is a MaxMind test fixture IP resolving to GB — deliberately
	// not "ID", the value the request body (above) tries to smuggle in.
	req.RemoteAddr = "81.2.69.142:1234"

	w := httptest.NewRecorder()
	organization.NewModuleEngineWithCountryResolver(pool, testAuthSub, resolver).ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data := resp["data"].(map[string]any)
	if got := data["country_code"]; got != "GB" {
		t.Errorf("want country_code resolved from GeoIP (GB), got %v — a client-supplied value must never win", got)
	}
}

// TestIntegration_CreateOrganization_UnresolvableIPFallsBackToUS covers the
// GeoIP-miss path (1.1.1.1 has no data in the test fixture) — must fall
// back to "US", the same default this API has always used, not error out.
func TestIntegration_CreateOrganization_UnresolvableIPFallsBackToUS(t *testing.T) {
	pool := testPool(t)
	resolver := testGeoIPResolver(t)
	const slug = "integ-ws-geoip-miss"
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE slug = $1`, slug)
	})

	req := httpserver.JSONTestRequest(http.MethodPost, "/api/organizations",
		`{"slug":"`+slug+`","name":"Integ WS","plan":"solo","cycle":"monthly"}`)
	req.RemoteAddr = "1.1.1.1:1234"

	w := httptest.NewRecorder()
	organization.NewModuleEngineWithCountryResolver(pool, testAuthSub, resolver).ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data := resp["data"].(map[string]any)
	if got := data["country_code"]; got != "US" {
		t.Errorf("want US fallback on a GeoIP miss, got %v", got)
	}
}
