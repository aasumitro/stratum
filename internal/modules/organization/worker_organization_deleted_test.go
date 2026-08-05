package organization_test

import (
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

// TestHandleOrganizationDeleted_PurgesLogo confirms the logo cleanup runs
// here, off the synchronous deleteOrganization request path. Needs no real
// DB — HandleOrganizationDeleted never touches the pool, only the storage
// client — so a nil pool is safe here, same as the two no-op tests above.
func TestHandleOrganizationDeleted_PurgesLogo(t *testing.T) {
	orgID := uuid.New().String()

	fakeStorage := newFakeStorageServer()
	defer fakeStorage.srv.Close()
	mod := organization.New(nil, messaging.NoopPublisher{})
	mod.SetStorageClient(fakeStorage.client())

	body := encodeOrganizationDeleted(orgID)
	if err := mod.Worker.HandleOrganizationDeleted(t.Context(), body); err != nil {
		t.Fatalf("HandleOrganizationDeleted: %v", err)
	}

	deleted := fakeStorage.deletedPaths()
	wantLogo := "organization:" + orgID + "/logo"
	if !slices.Contains(deleted, wantLogo) {
		t.Errorf("expected a storage delete for %q, got %v", wantLogo, deleted)
	}

	// Redelivery: a repeated logo delete on an already-missing object is a
	// safe no-op — must not error.
	if err := mod.Worker.HandleOrganizationDeleted(t.Context(), body); err != nil {
		t.Fatalf("redelivery must not error: %v", err)
	}
}
