package organization_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/messaging"
	"github.com/aasumitro/stratum/internal/platform/storage"
)

func encodeOrganizationDeleted(orgID string) []byte {
	env := events.Envelope{
		ID: uuid.New().String(), Type: events.RoutingKeyOrganizationDeleted,
		Source: "organization", Time: time.Now(), OrgID: orgID,
		Data: events.OrganizationDeleted{OrganizationID: orgID, DeletedAt: time.Now()},
	}
	b, _ := json.Marshal(env)
	return b
}

// TestHandleOrganizationDeleted_MalformedBody_NoOp and
// TestHandleOrganizationDeleted_NilStore_NoOp need no DB: both return before
// ever touching the pool, so a nil pool is safe to pass here.

func TestHandleOrganizationDeleted_MalformedBody_NoOp(t *testing.T) {
	mod := organization.New(nil, messaging.NoopPublisher{})
	if err := mod.Worker.HandleOrganizationDeleted(t.Context(), []byte("not json")); err != nil {
		t.Fatalf("malformed body should not error (don't re-queue a bad envelope): %v", err)
	}
}

func TestHandleOrganizationDeleted_NilStore_NoOp(t *testing.T) {
	mod := organization.New(nil, messaging.NoopPublisher{}) // SetStorageClient never called
	body := encodeOrganizationDeleted(uuid.New().String())
	if err := mod.Worker.HandleOrganizationDeleted(t.Context(), body); err != nil {
		t.Fatalf("unconfigured storage should no-op, not error: %v", err)
	}
}

// fakeStorageServer records every DELETE it receives (bucket parsed from the
// URL, paths from the request body) so the test can assert exactly what
// HandleOrganizationDeleted attempted to clean up, without a real Supabase
// Storage instance.
type fakeStorageServer struct {
	mu      sync.Mutex
	deletes []string // "bucket:path"
	srv     *httptest.Server
}

func newFakeStorageServer() *fakeStorageServer {
	f := &fakeStorageServer{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		bucket := strings.TrimPrefix(r.URL.Path, "/storage/v1/object/")
		var body struct {
			Prefixes []string `json:"prefixes"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		for _, p := range body.Prefixes {
			f.deletes = append(f.deletes, bucket+":"+p)
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	return f
}

func (f *fakeStorageServer) client() *storage.Client {
	return storage.New(storage.Config{BaseURL: f.srv.URL, Key: "test"})
}

func (f *fakeStorageServer) deletedPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deletes...)
}

// TestIntegration_HandleOrganizationDeleted_PurgesFilesAndLogo confirms the
// storage cleanup runs here, off the synchronous deleteOrganization request
// path.
func TestIntegration_HandleOrganizationDeleted_PurgesFilesAndLogo(t *testing.T) {
	pool := testPool(t)
	orgID := uuid.New().String()
	path1 := orgID + "/file-1"
	path2 := orgID + "/file-2"

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
	})

	if _, err := pool.Exec(t.Context(),
		`INSERT INTO organization.organizations (id, slug, name, owner_id) VALUES ($1, $2, $3, $4)`,
		orgID, "worker-del-"+orgID[:8], "Worker Delete Test", testAuthSub); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	for _, p := range []string{path1, path2} {
		if _, err := pool.Exec(t.Context(),
			`INSERT INTO organization.files (organization_id, name, path, size_bytes, created_by) VALUES ($1, $2, $3, 10, $4)`,
			orgID, p, p, testAuthSub); err != nil {
			t.Fatalf("seed file: %v", err)
		}
	}

	fakeStorage := newFakeStorageServer()
	defer fakeStorage.srv.Close()
	mod := organization.New(pool, messaging.NoopPublisher{})
	mod.SetStorageClient(fakeStorage.client())

	body := encodeOrganizationDeleted(orgID)
	if err := mod.Worker.HandleOrganizationDeleted(t.Context(), body); err != nil {
		t.Fatalf("HandleOrganizationDeleted: %v", err)
	}

	var count int
	pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM organization.files WHERE organization_id = $1`, orgID).Scan(&count)
	if count != 0 {
		t.Errorf("want all files removed from DB, %d remain", count)
	}

	deleted := fakeStorage.deletedPaths()
	wantLogo := "organization:" + orgID + "/logo"
	wantFile1 := "organization-files:" + path1
	wantFile2 := "organization-files:" + path2
	for _, want := range []string{wantLogo, wantFile1, wantFile2} {
		if !slices.Contains(deleted, want) {
			t.Errorf("expected a storage delete for %q, got %v", want, deleted)
		}
	}

	// Redelivery: deleteAllFilesForOrganization now finds nothing (rows
	// already gone) — must not error, and must not attempt to delete
	// per-file paths a second time (the logo delete alone is harmless to
	// repeat, storage.Delete on an already-missing object is a no-op).
	if err := mod.Worker.HandleOrganizationDeleted(t.Context(), body); err != nil {
		t.Fatalf("redelivery must not error: %v", err)
	}
	redeliveredDeletes := fakeStorage.deletedPaths()
	fileDeletesAfterRedelivery := 0
	for _, got := range redeliveredDeletes {
		if got == wantFile1 || got == wantFile2 {
			fileDeletesAfterRedelivery++
		}
	}
	if fileDeletesAfterRedelivery != 2 {
		t.Errorf("want exactly 2 total file-delete calls across both deliveries (no re-attempt on redelivery), got %d", fileDeletesAfterRedelivery)
	}
}
