package reference_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/reference"
)

func testPoolReference(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// --- contracts.CountryTaxReader: GetCountryTaxRate ---
//
// Catalog method tests (ListPlans/GetPlanByID/ListFeatures/ListAddons)
// are in billing/repository_test.go — billing owns that data, see
// billing/repository.go's "catalog" section.

func TestIntegration_GetCountryTaxRate_US(t *testing.T) {
	mod := reference.NewModuleForTest(testPoolReference(t))

	rate, err := mod.GetCountryTaxRate(t.Context(), "US")
	if err != nil {
		t.Fatalf("GetCountryTaxRate US: %v", err)
	}
	if rate != 0 {
		t.Errorf("want 0 BPS for US, got %d", rate)
	}
}

func TestIntegration_GetCountryTaxRate_ID(t *testing.T) {
	mod := reference.NewModuleForTest(testPoolReference(t))

	rate, err := mod.GetCountryTaxRate(t.Context(), "ID")
	if err != nil {
		t.Fatalf("GetCountryTaxRate ID: %v", err)
	}
	if rate != 0 {
		t.Errorf("want 0 BPS for ID (tax included in plan price), got %d", rate)
	}
}

func TestIntegration_GetCountryTaxRate_Unknown(t *testing.T) {
	mod := reference.NewModuleForTest(testPoolReference(t))

	_, err := mod.GetCountryTaxRate(t.Context(), "XX")
	if err == nil {
		t.Error("want error for unknown country code, got nil")
	}
}

// --- HTTP endpoints ---

func TestIntegration_Plans_HTTPEndpoint(t *testing.T) {
	e := reference.NewModuleEngine(testPoolReference(t))

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/references/plans", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data, _ := resp["data"].([]any)
	if len(data) < 3 {
		t.Errorf("want >= 3 plans in response, got %d", len(data))
	}
	for _, p := range data {
		plan, _ := p.(map[string]any)
		if _, ok := plan["features"]; ok && plan["features"] == nil {
			t.Errorf("plan %v: want features:[] in JSON, got features:null (crashes frontend .slice()/.map() calls)", plan["id"])
		}
	}
}

func TestIntegration_Features_HTTPEndpoint(t *testing.T) {
	e := reference.NewModuleEngine(testPoolReference(t))

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/references/features", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data, _ := resp["data"].([]any)
	if len(data) < 12 {
		t.Errorf("want >= 12 features in response, got %d", len(data))
	}
}

func TestIntegration_Addons_HTTPEndpoint(t *testing.T) {
	e := reference.NewModuleEngine(testPoolReference(t))

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/references/addons", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data, _ := resp["data"].([]any)
	if len(data) < 2 {
		t.Errorf("want >= 2 addons in response, got %d", len(data))
	}
}

func TestIntegration_Countries_HTTPEndpoint(t *testing.T) {
	e := reference.NewModuleEngine(testPoolReference(t))

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/references/countries", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data, _ := resp["data"].([]any)
	if len(data) < 1 {
		t.Error("want >= 1 country in response")
	}
}

func TestIntegration_Currencies_HTTPEndpoint(t *testing.T) {
	e := reference.NewModuleEngine(testPoolReference(t))

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/references/currencies", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data, _ := resp["data"].([]any)
	if len(data) < 1 {
		t.Error("want >= 1 currency in response")
	}
}
