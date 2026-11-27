package app

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/audit"
	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/testsupport"
)

func TestNewAuditArchive(t *testing.T) {
	bucket := func(status int, body string) string {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(server.Close)
		return server.URL
	}
	for name, c := range map[string]struct {
		env, endpoint string
		wantArchive   bool
		wantErr       error
		wantLog       string
	}{
		"off in development":     {"development", "", false, nil, ""},
		"incomplete":             {"production", "https://s3.example.com/", false, nil, ""},
		"off elsewhere":          {"production", "", false, nil, "not copied to write-once storage"},
		"locked bucket":          {"production", bucket(200, `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`), true, nil, ""},
		"bucket without a lock":  {"production", bucket(404, `<Error><Code>ObjectLockConfigurationNotFoundError</Code></Error>`), false, audit.ErrArchiveNotLocked, ""},
		"refused credentials":    {"production", bucket(403, `<Error><Code>AccessDenied</Code></Error>`), true, nil, "could not be checked"},
		"nothing at the address": {"production", "http://127.0.0.1:1", true, nil, "could not be checked"},
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			cfg := &config.Config{Env: c.env, AuditRetentionDays: 400, Archive: config.AuditArchiveConfig{
				Endpoint: c.endpoint, Region: "us-east-1", Bucket: "audit", AccessKey: "id", SecretKey: "sec-ret", RetentionDays: 400,
			}}
			if name == "incomplete" {
				cfg.Archive.Bucket = ""
			}
			archive, err := newAuditArchive(t.Context(), cfg, nil, slog.New(slog.NewTextHandler(&logs, nil)))
			if name == "incomplete" {
				if err == nil {
					t.Error("want the missing setting refused")
				}
				return
			}
			if !errors.Is(err, c.wantErr) || (archive != nil) != c.wantArchive {
				t.Errorf("archive = %v, %v", archive, err)
			}
			if c.wantLog == "" && logs.Len() != 0 || !strings.Contains(logs.String(), c.wantLog) {
				t.Errorf("log %q, want %q", logs.String(), c.wantLog)
			}
			if strings.Contains(logs.String(), "sec-ret") {
				t.Errorf("the log carries a credential: %s", logs.String())
			}
		})
	}
}

func TestArchiveGauge(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	reader := testsupport.ManualMetricReader(t)
	archive := audit.NewArchive(pool, audit.ArchiveStoreConfig{Endpoint: "http://127.0.0.1:1"}, slog.Default(), 400, 400)

	registration, err := registerArchiveGauge(archive, slog.Default())
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { _ = registration.Unregister() })

	if _, ok := testsupport.GaugeValue(t, reader, "stratum.audit.archive.pending"); !ok {
		t.Error("the gauge reports nothing")
	}

	// A count that cannot be read is logged and skipped, never reported as zero.
	pool.Close()
	if got, ok := testsupport.GaugeValue(t, reader, "stratum.audit.archive.pending"); ok {
		t.Errorf("reported %d without a database", got)
	}
}

func TestVerifyAuditArchive_ExitsTellAFindingFromAnOutage(t *testing.T) {
	t.Setenv("POSTGRES_WORKER_URL", "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	t.Setenv("AUDIT_ARCHIVE_ENDPOINT", "")
	if err := VerifyAuditArchive(t.Context(), io.Discard, nil); err == nil || errors.Is(err, ErrArchiveNotVerified) {
		t.Errorf("no archive configured: %v, want an error that is not a finding", err)
	}
	for name, value := range map[string]string{
		"AUDIT_ARCHIVE_ENDPOINT": "http://127.0.0.1:1", "AUDIT_ARCHIVE_BUCKET": "audit",
		"AUDIT_ARCHIVE_ACCESS_KEY": "id", "AUDIT_ARCHIVE_SECRET_KEY": "secret",
	} {
		t.Setenv(name, value)
	}
	if err := VerifyAuditArchive(t.Context(), io.Discard, nil); err == nil || errors.Is(err, ErrArchiveNotVerified) {
		t.Errorf("nothing reachable: %v, want an error that is not a finding", err)
	}
}
