package storage_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aasumitro/stratum/internal/platform/storage"
)

func TestNew_TrimsTrailingSlash(t *testing.T) {
	c := storage.New(storage.Config{BaseURL: "https://proj.supabase.co/", Key: "k"})
	if c.BaseURL() != "https://proj.supabase.co" {
		t.Errorf("BaseURL = %q, want trailing slash trimmed", c.BaseURL())
	}
}

func TestUpload_SendsAuthAndUpsert(t *testing.T) {
	var gotAuth, gotAPIKey, gotUpsert, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("apikey")
		gotUpsert = r.Header.Get("x-upsert")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := storage.New(storage.Config{BaseURL: srv.URL, Key: "svc-role-key"})
	err := c.Upload(t.Context(), "org-files", "a/b/logo.png", strings.NewReader("PNGDATA"), "image/png")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if gotAuth != "Bearer svc-role-key" || gotAPIKey != "svc-role-key" {
		t.Errorf("auth headers not set: auth=%q apikey=%q", gotAuth, gotAPIKey)
	}
	if gotUpsert != "true" {
		t.Errorf("x-upsert header = %q, want true", gotUpsert)
	}
	if gotBody != "PNGDATA" {
		t.Errorf("uploaded body = %q, want PNGDATA", gotBody)
	}
}

func TestUpload_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	c := storage.New(storage.Config{BaseURL: srv.URL, Key: "k"})
	if err := c.Upload(t.Context(), "b", "p", strings.NewReader("x"), "text/plain"); err == nil {
		t.Error("expected an error on 500 upload")
	}
}

func TestSignedURL_PrefixesRelativePath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"signedURL": "/object/sign/b/p?token=abc"})
	}))
	defer srv.Close()

	c := storage.New(storage.Config{BaseURL: srv.URL, Key: "k"})
	got, err := c.SignedURL(t.Context(), "b", "p", 5*time.Minute)
	if err != nil {
		t.Fatalf("SignedURL: %v", err)
	}
	want := srv.URL + "/object/sign/b/p?token=abc"
	if got != want {
		t.Errorf("SignedURL = %q, want %q (relative path prefixed with base)", got, want)
	}
}

func TestSignedURL_AbsoluteReturnedAsIs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"signedURL": "https://cdn.example.com/x?token=z"})
	}))
	defer srv.Close()

	c := storage.New(storage.Config{BaseURL: srv.URL, Key: "k"})
	got, _ := c.SignedURL(t.Context(), "b", "p", time.Minute)
	if got != "https://cdn.example.com/x?token=z" {
		t.Errorf("absolute signed URL should be returned unchanged, got %q", got)
	}
}

func TestSignedURL_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := storage.New(storage.Config{BaseURL: srv.URL, Key: "k"})
	if _, err := c.SignedURL(t.Context(), "b", "missing", time.Minute); err == nil {
		t.Error("expected error for 404 sign")
	}
}

func TestDelete(t *testing.T) {
	var method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := storage.New(storage.Config{BaseURL: srv.URL, Key: "k"})
	if err := c.Delete(t.Context(), "b", "p"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if method != http.MethodDelete {
		t.Errorf("Delete used %s, want DELETE", method)
	}
}

func TestEnsureBuckets(t *testing.T) {
	// server behavior keyed by the bucket id in the request body
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		switch body["id"] {
		case "fresh":
			w.WriteHeader(http.StatusOK)
		case "conflict":
			w.WriteHeader(http.StatusConflict) // real HTTP 409
		case "dup-in-body":
			// Supabase quirk: 400 with an embedded "statusCode":"409"
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"statusCode":"409","error":"Duplicate"}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"Bad","message":"nope"}`))
		}
	}))
	defer srv.Close()
	c := storage.New(storage.Config{BaseURL: srv.URL, Key: "k"})

	// all three "already exists"/"created" shapes are treated as success
	if err := c.EnsureBuckets(t.Context(), []storage.BucketConfig{
		{Name: "fresh", Public: true, FileSizeLimit: 1024},
		{Name: "conflict"},
		{Name: "dup-in-body"},
	}); err != nil {
		t.Fatalf("EnsureBuckets idempotent shapes: %v", err)
	}

	// a genuine error surfaces
	if err := c.EnsureBuckets(t.Context(), []storage.BucketConfig{{Name: "boom"}}); err == nil {
		t.Error("expected EnsureBuckets to surface a non-duplicate 400")
	}
}
