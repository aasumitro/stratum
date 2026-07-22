package middleware_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	jwtgo "github.com/golang-jwt/jwt/v5"

	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
)

const testKID = "test-key-1"

// jwksFixture spins up a JWKS endpoint backed by a fresh RSA key and returns
// the key + server so tests can mint tokens the middleware will accept.
func jwksFixture(t *testing.T) (*rsa.PrivateKey, *httptest.Server) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	b64 := base64.RawURLEncoding
	jwks := map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"kid": testKID,
			"use": "sig",
			"alg": "RS256",
			"n":   b64.EncodeToString(key.N.Bytes()),
			"e":   b64.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(srv.Close)
	return key, srv
}

func signToken(t *testing.T, key *rsa.PrivateKey, claims jwtgo.MapClaims) string {
	t.Helper()
	tok := jwtgo.NewWithClaims(jwtgo.SigningMethodRS256, claims)
	tok.Header["kid"] = testKID
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

// authEngine mounts /me behind the strict, header-only middleware — the
// variant every route except the SSE stream must use.
func authEngine(t *testing.T, jwksURL string, hooks ...middleware.AuthHooks) *gin.Engine {
	t.Helper()
	mw, _, err := middleware.NewAuthMiddleware(t.Context(), config.AuthConfig{JWKSURL: jwksURL}, hooks...)
	if err != nil {
		t.Fatalf("NewAuthMiddleware: %v", err)
	}
	return mountAuthEngine(mw)
}

// authSSEEngine mounts /me behind the cookie-accepting variant — only the
// SSE stream route should ever use this.
func authSSEEngine(t *testing.T, jwksURL string, hooks ...middleware.AuthHooks) *gin.Engine {
	t.Helper()
	_, mw, err := middleware.NewAuthMiddleware(t.Context(), config.AuthConfig{JWKSURL: jwksURL}, hooks...)
	if err != nil {
		t.Fatalf("NewAuthMiddleware: %v", err)
	}
	return mountAuthEngine(mw)
}

func mountAuthEngine(mw gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.GET("/me", mw, func(c *gin.Context) {
		claims, _ := middleware.ClaimsFromContext(c)
		c.JSON(http.StatusOK, gin.H{"sub": claims.Subject})
	})
	return e
}

func TestAuth_ValidToken_Passes(t *testing.T) {
	key, srv := jwksFixture(t)
	e := authEngine(t, srv.URL)

	token := signToken(t, key, jwtgo.MapClaims{"sub": "user-123", "exp": time.Now().Add(time.Hour).Unix()})
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("valid token: want 200, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["sub"] != "user-123" {
		t.Errorf("subject not propagated: %v", resp)
	}
}

func TestAuth_MissingHeader_401(t *testing.T) {
	_, srv := jwksFixture(t)
	e := authEngine(t, srv.URL)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/me", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("missing header: want 401, got %d", w.Code)
	}
}

func TestAuth_MalformedAndExpiredTokens_401(t *testing.T) {
	key, srv := jwksFixture(t)
	e := authEngine(t, srv.URL)

	// garbage token
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer not.a.jwt")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("garbage token: want 401, got %d", w.Code)
	}

	// expired token
	expired := signToken(t, key, jwtgo.MapClaims{"sub": "u", "exp": time.Now().Add(-time.Hour).Unix()})
	req2 := httptest.NewRequest(http.MethodGet, "/me", nil)
	req2.Header.Set("Authorization", "Bearer "+expired)
	w2 := httptest.NewRecorder()
	e.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("expired token: want 401, got %d", w2.Code)
	}
}

func TestAuth_EmptySubject_401(t *testing.T) {
	key, srv := jwksFixture(t)
	e := authEngine(t, srv.URL)
	token := signToken(t, key, jwtgo.MapClaims{"sub": "", "exp": time.Now().Add(time.Hour).Unix()})
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("empty sub: want 401, got %d", w.Code)
	}
}

func TestAuth_RevokedToken_401(t *testing.T) {
	key, srv := jwksFixture(t)
	hooks := middleware.AuthHooks{
		IsRevoked: func(_ context.Context, sessionID string) bool { return sessionID == "revoked-jti" },
	}
	e := authEngine(t, srv.URL, hooks)

	// jti is checked only as a fallback (no session_id claim present here) —
	// see TestAuth_RevokedToken_BySessionID_401 for the primary path, which
	// is what a real Supabase token actually carries.
	token := signToken(t, key, jwtgo.MapClaims{"sub": "u", "jti": "revoked-jti", "exp": time.Now().Add(time.Hour).Unix()})
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("revoked token: want 401, got %d: %s", w.Code, w.Body)
	}
}

// TestAuth_RevokedToken_BySessionID_401 regression-tests revocation on a
// token shaped like a real Supabase access token — session_id, not jti
// (confirmed by decoding a real token issued for this project's test
// account; Supabase never sends jti). Before this fix, IsRevoked was only
// ever called with the jti claim, so a token like this one — no jti at all
// — silently never had its revocation checked, regardless of what
// IsRevoked itself returned.
func TestAuth_RevokedToken_BySessionID_401(t *testing.T) {
	key, srv := jwksFixture(t)
	hooks := middleware.AuthHooks{
		IsRevoked: func(_ context.Context, sessionID string) bool { return sessionID == "revoked-session" },
	}
	e := authEngine(t, srv.URL, hooks)

	token := signToken(t, key, jwtgo.MapClaims{"sub": "u", "session_id": "revoked-session", "exp": time.Now().Add(time.Hour).Unix()})
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("revoked token (session_id claim): want 401, got %d: %s", w.Code, w.Body)
	}
}

// TestAuth_SessionIDTakesPriorityOverJTI regression-tests that when both
// claims are present, revocation is keyed by session_id, not jti — matching
// the real-token shape (Supabase) this project actually authenticates
// against, with jti reserved as a fallback for some other IdP that might
// issue only that claim.
func TestAuth_SessionIDTakesPriorityOverJTI(t *testing.T) {
	key, srv := jwksFixture(t)
	var checkedWith string
	hooks := middleware.AuthHooks{
		IsRevoked: func(_ context.Context, sessionID string) bool {
			checkedWith = sessionID
			return false
		},
	}
	e := authEngine(t, srv.URL, hooks)

	token := signToken(t, key, jwtgo.MapClaims{
		"sub": "u", "session_id": "the-session-id", "jti": "the-jti", "exp": time.Now().Add(time.Hour).Unix(),
	})
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if checkedWith != "the-session-id" {
		t.Errorf("IsRevoked called with %q, want session_id (\"the-session-id\") to take priority over jti", checkedWith)
	}
}

func TestAuth_CookieFallback_Passes(t *testing.T) {
	key, srv := jwksFixture(t)
	e := authSSEEngine(t, srv.URL)

	// EventSource can't set headers → the SSE variant falls back to __session cookie
	token := signToken(t, key, jwtgo.MapClaims{"sub": "cookie-user", "exp": time.Now().Add(time.Hour).Unix()})
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.AddCookie(&http.Cookie{Name: "__session", Value: token})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("cookie fallback: want 200, got %d: %s", w.Code, w.Body)
	}
}

// TestAuth_CookieFallback_RejectedOnStrictVariant guards against the cookie
// fallback silently widening back to every route — only the SSE stream
// route should ever accept __session.
func TestAuth_CookieFallback_RejectedOnStrictVariant(t *testing.T) {
	key, srv := jwksFixture(t)
	e := authEngine(t, srv.URL)

	token := signToken(t, key, jwtgo.MapClaims{"sub": "cookie-user", "exp": time.Now().Add(time.Hour).Unix()})
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.AddCookie(&http.Cookie{Name: "__session", Value: token})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("cookie on strict variant: want 401, got %d: %s", w.Code, w.Body)
	}
}

func TestNewAuthMiddleware_BadJWKSURL_Errors(t *testing.T) {
	if _, _, err := middleware.NewAuthMiddleware(t.Context(), config.AuthConfig{JWKSURL: "://bad"}); err == nil {
		t.Error("expected an error for an invalid JWKS URL")
	}
}
