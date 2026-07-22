package reference_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aasumitro/stratum/internal/modules/reference"
)

func TestListCountries_ReachesService(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	reference.NewHandlerEngine().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/references/countries", nil))
}

func TestListCurrencies_ReachesService(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	reference.NewHandlerEngine().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/references/currencies", nil))
}

func TestListPlans_ReachesService(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	reference.NewHandlerEngine().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/references/plans", nil))
}

func TestListFeatures_ReachesService(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	reference.NewHandlerEngine().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/references/features", nil))
}

func TestListAddons_ReachesService(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	reference.NewHandlerEngine().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/references/addons", nil))
}
