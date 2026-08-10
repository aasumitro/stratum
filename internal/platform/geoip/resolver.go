// Package geoip resolves a request's country from its IP address via a
// local MaxMind GeoLite2/GeoIP2 Country database. It exists to be the one
// trusted source of billing country/currency (see internal/modules/
// organization and internal/modules/reference, its two callers) — an IP
// address isn't something a client can set directly, unlike a request field.
package geoip

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"

	geoip2 "github.com/oschwald/geoip2-golang/v2"
)

// DebugCountryCodeHeader lets a caller simulate any billing country in
// development, where GeoIP can never resolve a loopback/private address —
// Resolve ignores it entirely outside development (see CountryCode).
const DebugCountryCodeHeader = "X-Debug-Country-Code"

// FallbackCountryCode is the country Resolve reports when nothing else
// resolves — the same default POST /organizations has always used.
const FallbackCountryCode = "US"

// Resolver looks up an IP address's ISO 3166-1 alpha-2 country code from a
// local GeoIP database. db is nil when GEOIP_DB_PATH is unset — only valid
// in development (see config.RequireGeoIPDBOutsideDev), where CountryCode's
// debugHeader override is the only path callers actually exercise.
type Resolver struct {
	db    *geoip2.Reader
	isDev bool
}

// New opens dbPath as a GeoIP Country database. dbPath may be empty only in
// development — isDev gates whether CountryCode honors its debugHeader
// override, independently of whether a path was given, so callers must pass
// both rather than one being inferred from the other.
func New(dbPath string, isDev bool) (*Resolver, error) {
	if dbPath == "" {
		return &Resolver{isDev: isDev}, nil
	}
	db, err := geoip2.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("geoip.New: open %q: %w", dbPath, err)
	}
	return &Resolver{db: db, isDev: isDev}, nil
}

// Close releases the underlying database file. No-op if unopened.
func (r *Resolver) Close() error {
	if r.db == nil {
		return nil
	}
	return r.db.Close()
}

// CountryCode resolves clientIP to an ISO 3166-1 alpha-2 country code.
// debugHeader, when non-empty, wins outright — but only in development:
// GeoIP can never resolve a loopback/private address, so local dev needs a
// deterministic way to simulate any country, and outside development this
// path must stay completely inert or it reopens the exact client-spoofing
// hole this package exists to close. Returns "" if nothing resolves —
// callers decide the final default and whether to log it.
func (r *Resolver) CountryCode(clientIP, debugHeader string) string {
	if r.isDev && debugHeader != "" {
		return strings.ToUpper(debugHeader)
	}
	if r.db == nil {
		return ""
	}
	addr, err := netip.ParseAddr(clientIP)
	if err != nil {
		return ""
	}
	record, err := r.db.Country(addr)
	if err != nil || !record.Country.HasData() {
		return ""
	}
	return record.Country.ISOCode
}

// Resolve is CountryCode plus this codebase's one fallback policy: when
// nothing resolves — a nil Resolver (dependency unwired), a GeoIP miss, or
// a non-development caller with no debug header — it returns
// FallbackCountryCode and logs a warning, so a client silently getting the
// default currency is never invisible. The nil check makes Resolve safe to
// call on an unwired *Resolver directly, matching this codebase's nil-safe
// optional-dependency convention.
func (r *Resolver) Resolve(ctx context.Context, clientIP, debugHeader string) string {
	if r != nil {
		if cc := r.CountryCode(clientIP, debugHeader); cc != "" {
			return cc
		}
	}
	slog.WarnContext(ctx, "geoip: country resolution fell back to default",
		"client_ip", clientIP, "fallback", FallbackCountryCode)
	return FallbackCountryCode
}
