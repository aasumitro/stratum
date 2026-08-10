package geoip_test

import (
	"context"
	"testing"

	"github.com/aasumitro/stratum/internal/platform/geoip"
)

// testDBPath is MaxMind's own published test fixture (github.com/maxmind/
// MaxMind-DB, test-data/GeoIP2-Country-Test.mmdb) — 81.2.69.142 and
// 2.125.160.216 are its well-known "resolves to GB" test IPs; 1.1.1.1 has
// no data in it at all, the deliberate miss case.
const testDBPath = "testdata/GeoIP2-Country-Test.mmdb"

func TestNew_EmptyPathInDev(t *testing.T) {
	r, err := geoip.New("", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := r.CountryCode("81.2.69.142", ""); got != "" {
		t.Errorf("unopened db: want no lookup result, got %q", got)
	}
}

func TestNew_InvalidPath(t *testing.T) {
	if _, err := geoip.New("testdata/does-not-exist.mmdb", false); err == nil {
		t.Error("want an error opening a nonexistent db, got nil")
	}
}

func TestNew_RealFile(t *testing.T) {
	r, err := geoip.New(testDBPath, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := r.CountryCode("81.2.69.142", ""); got != "GB" {
		t.Errorf("want GB, got %q", got)
	}
}

func TestResolver_CountryCode_UnknownIP(t *testing.T) {
	r, err := geoip.New(testDBPath, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := r.CountryCode("1.1.1.1", ""); got != "" {
		t.Errorf("IP with no data in the test db: want empty, got %q", got)
	}
}

func TestResolver_CountryCode_InvalidIP(t *testing.T) {
	r, err := geoip.New(testDBPath, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := r.CountryCode("not-an-ip", ""); got != "" {
		t.Errorf("unparseable IP: want empty, got %q", got)
	}
}

func TestResolver_CountryCode_DebugHeaderOverridesInDev(t *testing.T) {
	r, err := geoip.New(testDBPath, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// clientIP resolves to GB via GeoIP, but the debug header must win in dev.
	if got := r.CountryCode("81.2.69.142", "id"); got != "ID" {
		t.Errorf("dev debug header: want ID (uppercased), got %q", got)
	}
}

func TestResolver_CountryCode_DebugHeaderIgnoredOutsideDev(t *testing.T) {
	r, err := geoip.New(testDBPath, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// A non-dev caller sending the debug header must never influence the
	// result — this is the property that keeps the header from becoming a
	// production client-spoofing backdoor.
	if got := r.CountryCode("81.2.69.142", "ID"); got != "GB" {
		t.Errorf("non-dev: debug header must be ignored, want GB (real GeoIP result), got %q", got)
	}
}

func TestResolver_Resolve_NilReceiverFallsBackToUS(t *testing.T) {
	var r *geoip.Resolver
	if got := r.Resolve(t.Context(), "81.2.69.142", ""); got != geoip.FallbackCountryCode {
		t.Errorf("nil resolver: want fallback %q, got %q", geoip.FallbackCountryCode, got)
	}
}

func TestResolver_Resolve_MissFallsBackToUS(t *testing.T) {
	r, err := geoip.New(testDBPath, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := r.Resolve(context.Background(), "1.1.1.1", ""); got != geoip.FallbackCountryCode {
		t.Errorf("GeoIP miss: want fallback %q, got %q", geoip.FallbackCountryCode, got)
	}
}

func TestResolver_Resolve_KnownIPReturnsResolvedCountry(t *testing.T) {
	r, err := geoip.New(testDBPath, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := r.Resolve(context.Background(), "81.2.69.142", ""); got != "GB" {
		t.Errorf("want GB, got %q", got)
	}
}
