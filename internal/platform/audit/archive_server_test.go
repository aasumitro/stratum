package audit

import (
	"context"
	"crypto/md5" //nolint:gosec // the server requires Content-MD5 on these calls
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// These tests run against a real S3 server, the one `make audit-archive-up` starts,
// and prove what a fake cannot: that the server itself refuses. They leave locked
// objects behind, which is the point, so they run only when asked to.
func serverStore(t *testing.T, identity, bucket string) *s3Store {
	t.Helper()
	endpoint := os.Getenv("AUDIT_ARCHIVE_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("AUDIT_ARCHIVE_TEST_ENDPOINT not set")
	}
	credential := "stratum-archive-" + identity
	return newS3Store(ArchiveStoreConfig{
		Endpoint: endpoint, Region: "us-east-1", Bucket: bucket, AccessKey: credential, SecretKey: credential,
	})
}

func wantForbidden(t *testing.T, what string, err error) {
	t.Helper()
	if answered, ok := errors.AsType[*s3Error](err); !ok || answered.Status != http.StatusForbidden {
		t.Errorf("%s: %v, want it refused", what, err)
	}
}

func TestArchiveServer_ALockedObjectCannotBeChangedByTheAdministrator(t *testing.T) {
	admin := serverStore(t, "admin", "stratum-audit")
	writer := serverStore(t, "writer", "stratum-audit")
	ctx := t.Context()
	key := fmt.Sprintf("acceptance/%d", time.Now().UnixNano())
	until := time.Now().Add(10 * time.Minute)

	version, created, err := writer.PutLocked(ctx, key, []byte("original"), until)
	if err != nil || !created || version == "" {
		t.Fatalf("put: %q, %v, %v", version, created, err)
	}
	if _, created, err := writer.PutLocked(ctx, key, []byte("second"), until); err != nil || created {
		t.Errorf("second put: %v, %v, want it turned down", created, err)
	}
	one := url.Values{"versionId": {version}}
	_, _, err = admin.do(ctx, http.MethodDelete, key, one, nil, nil)
	wantForbidden(t, "delete", err)
	_, _, err = admin.do(ctx, http.MethodDelete, key, one, http.Header{"X-Amz-Bypass-Governance-Retention": {"true"}}, nil)
	wantForbidden(t, "delete with bypass", err)
	for what, retention := range map[string]struct {
		mode  string
		until time.Time
	}{
		"shorter lock": {lockModeCompliance, time.Now().Add(time.Minute)},
		"weaker mode":  {"GOVERNANCE", until},
	} {
		body := fmt.Appendf(nil, `<Retention xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Mode>%s</Mode><RetainUntilDate>%s</RetainUntilDate></Retention>`,
			retention.mode, retention.until.UTC().Format(time.RFC3339))
		sum := md5.Sum(body) //nolint:gosec // see the import
		_, _, err := admin.do(ctx, http.MethodPut, key, url.Values{"versionId": {version}, "retention": {""}}, http.Header{
			"Content-Md5":                       {base64.StdEncoding.EncodeToString(sum[:])},
			"X-Amz-Bypass-Governance-Retention": {"true"},
		}, body)
		wantForbidden(t, what, err)
	}

	// A plain write and a plain delete go through, on top of the original.
	if _, _, err := writer.do(ctx, http.MethodPut, key, nil, nil, []byte("written over")); err != nil {
		t.Fatalf("plain put: %v", err)
	}
	if _, _, err := admin.do(ctx, http.MethodDelete, key, nil, nil, nil); err != nil {
		t.Fatalf("plain delete: %v", err)
	}
	versions, err := writer.Versions(ctx, key)
	if err != nil || len(versions) != 3 {
		t.Fatalf("versions: %v, %v, want the marker, the second write and the original", versions, err)
	}
	if !versions[0].IsDeleteMarker || versions[2].VersionID != version {
		t.Errorf("versions = %v, want the newest first and the original last", versions)
	}
	object, err := writer.Get(ctx, key, version)
	if err != nil || string(object.Body) != "original" || object.Mode != lockModeCompliance ||
		object.RetainUntil.Before(until.Truncate(time.Second)) {
		t.Errorf("original = %+v, %v", object, err)
	}
}

func TestArchiveServer_TheVerifierReadsAndCannotWrite(t *testing.T) {
	writer := serverStore(t, "writer", "stratum-audit")
	verifier := serverStore(t, "verifier", "stratum-audit")
	key := fmt.Sprintf("acceptance/%d", time.Now().UnixNano())
	version, _, err := writer.PutLocked(t.Context(), key, []byte("original"), time.Now().Add(10*time.Minute))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	for _, store := range []*s3Store{writer, verifier} {
		if enabled, err := store.LockEnabled(t.Context()); err != nil || !enabled {
			t.Errorf("lock: %v, %v", enabled, err)
		}
	}
	if object, err := verifier.Get(t.Context(), key, version); err != nil || object.Mode != lockModeCompliance {
		t.Errorf("get: %+v, %v", object, err)
	}
	if versions, err := verifier.Versions(t.Context(), key); err != nil || len(versions) != 1 {
		t.Errorf("versions: %v, %v", versions, err)
	}
	_, _, err = verifier.PutLocked(t.Context(), key+"x", []byte("x"), time.Now().Add(time.Minute))
	wantForbidden(t, "put as the verifier", err)
	_, _, err = verifier.do(t.Context(), http.MethodDelete, key, nil, nil, nil)
	wantForbidden(t, "delete as the verifier", err)
}

func TestArchiveServer_ExportAndVerify(t *testing.T) {
	bucket := fmt.Sprintf("audit-test-%d", time.Now().UnixNano())
	store := serverStore(t, "admin", bucket)
	f := newArchiveFixture(t)
	f.useStore(store)

	if _, _, err := store.do(t.Context(), http.MethodPut, "", nil, nil, nil); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	if err := f.archive.CheckLock(t.Context()); !errors.Is(err, ErrArchiveNotLocked) {
		t.Errorf("a bucket without a lock: %v, want ErrArchiveNotLocked", err)
	}
	if _, _, err := store.do(t.Context(), http.MethodDelete, "", nil, nil, nil); err != nil {
		t.Fatalf("delete bucket: %v", err)
	}
	if _, _, err := store.do(t.Context(), http.MethodPut, "", nil,
		http.Header{"X-Amz-Bucket-Object-Lock-Enabled": {"true"}}, nil); err != nil {
		t.Fatalf("create locked bucket: %v", err)
	}

	f.seed(t, archivePerson)
	operator := f.seed(t, "operator:op_ana")
	f.export(t, 2)
	f.seed(t, archivePerson)
	f.export(t, 1)
	if report := f.verify(t); !report.OK() || report.Objects != 2 || report.Matched != 3 {
		t.Fatalf("report = %+v", report)
	}

	if _, err := f.admin.Exec(context.Background(), `UPDATE audit.events SET actor = 'operator:op_budi' WHERE id = $1`, operator); err != nil {
		t.Fatalf("owner's edit: %v", err)
	}
	if problems := f.verify(t).Problems(problemAltered); len(problems) != 1 || !strings.HasPrefix(problems[0], operator) {
		t.Errorf("problems = %v, want the edited row named", problems)
	}
}
