package audit

import (
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// The vectors are the examples AWS publishes for Signature Version 4 on S3, with its
// documented example credentials.
func TestSignS3_PublishedVectors(t *testing.T) {
	cfg := ArchiveStoreConfig{
		Region: "us-east-1", AccessKey: "AKIAIOSFODNN7EXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		method, path string
		query        url.Values
		header       http.Header
		body, want   string
	}{
		"get object": {
			http.MethodGet, "/test.txt", nil, http.Header{"Range": {"bytes=0-9"}}, "",
			"f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41",
		},
		"put object": {
			http.MethodPut, "/test$file.text", nil,
			http.Header{"Date": {"Fri, 24 May 2013 00:00:00 GMT"}, "X-Amz-Storage-Class": {"REDUCED_REDUNDANCY"}},
			"Welcome to Amazon S3.",
			"98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd",
		},
		"query without a value": {
			http.MethodGet, "/", url.Values{"lifecycle": {""}}, nil, "",
			"fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543",
		},
		"query with values": {
			http.MethodGet, "/", url.Values{"prefix": {"J"}, "max-keys": {"2"}}, nil, "",
			"34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7",
		},
	} {
		t.Run(name, func(t *testing.T) {
			u := &url.URL{
				Scheme: "https", Host: "examplebucket.s3.amazonaws.com",
				Path: c.path, RawPath: escapeS3(c.path, true), RawQuery: canonicalQuery(c.query),
			}
			req, err := http.NewRequestWithContext(t.Context(), c.method, u.String(), strings.NewReader(c.body))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			maps.Copy(req.Header, c.header)
			signS3(req, []byte(c.body), cfg, now)
			if auth := req.Header.Get("Authorization"); !strings.HasSuffix(auth, "Signature="+c.want) {
				t.Errorf("Authorization = %q, want signature %s", auth, c.want)
			}
		})
	}
}

// fakeS3 answers each request with the next canned answer and keeps what it was asked.
type fakeS3 struct {
	t        *testing.T
	answers  []func(http.ResponseWriter)
	requests []*http.Request
	bodies   []string
}

func (f *fakeS3) store() *s3Store {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.requests = append(f.requests, r)
		f.bodies = append(f.bodies, string(body))
		if len(f.requests) > len(f.answers) {
			f.t.Errorf("unexpected request %s %s", r.Method, r.URL)
			return
		}
		f.answers[len(f.requests)-1](w)
	}))
	f.t.Cleanup(server.Close)
	return newS3Store(ArchiveStoreConfig{
		Endpoint: server.URL + "/", Region: "us-east-1", Bucket: "audit", AccessKey: "id", SecretKey: "secret",
	})
}

func answer(status int, body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestS3Store_LockEnabled(t *testing.T) {
	f := &fakeS3{t: t, answers: []func(http.ResponseWriter){
		answer(200, `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`),
		answer(404, `<Error><Code>ObjectLockConfigurationNotFoundError</Code></Error>`),
		answer(404, `<Error><Code>NoSuchBucket</Code></Error>`),
		answer(403, `<Error><Code>AccessDenied</Code></Error>`),
	}}
	store := f.store()
	if enabled, err := store.LockEnabled(t.Context()); err != nil || !enabled {
		t.Errorf("locked bucket: %v, %v", enabled, err)
	}
	if enabled, err := store.LockEnabled(t.Context()); err != nil || enabled {
		t.Errorf("bucket without a lock: %v, %v, want a definite no", enabled, err)
	}
	// A missing bucket or a refused question says nothing about the lock.
	for range 2 {
		if _, err := store.LockEnabled(t.Context()); err == nil {
			t.Error("want an error")
		}
	}
	if got := f.requests[0].URL.RequestURI(); got != "/audit?object-lock=" {
		t.Errorf("asked %s", got)
	}
}

func TestS3Store_PutLocked(t *testing.T) {
	f := &fakeS3{t: t, answers: []func(http.ResponseWriter){
		func(w http.ResponseWriter) { w.Header().Set("X-Amz-Version-Id", "v1") },
		answer(412, `<Error><Code>PreconditionFailed</Code></Error>`),
		answer(409, `<Error><Code>ConditionalRequestConflict</Code></Error>`),
		answer(409, `<Error><Code>ConditionalRequestConflict</Code></Error>`),
		answer(307, ""),
	}}
	store := f.store()
	until := time.Date(2027, 1, 2, 3, 4, 5, 0, time.FixedZone("WIB", 7*3600))

	version, created, err := store.PutLocked(t.Context(), "events/1.gz", []byte("content"), until)
	if err != nil || !created || version != "v1" {
		t.Fatalf("first put: %q, %v, %v", version, created, err)
	}
	req := f.requests[0]
	for name, want := range map[string]string{
		"If-None-Match": "*",
		headerMode:      "COMPLIANCE",
		headerUntil:     "2027-01-01T20:04:05Z",
		"Content-Md5":   "mgNkuembtIDdJeHwKEyFVQ==",
	} {
		if got := req.Header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
		if !strings.Contains(req.Header.Get("Authorization"), strings.ToLower(name)) {
			t.Errorf("%s is not signed", name)
		}
	}
	if req.Method != http.MethodPut || req.URL.Path != "/audit/events/1.gz" || f.bodies[0] != "content" {
		t.Errorf("sent %s %s %q", req.Method, req.URL.Path, f.bodies[0])
	}

	if _, created, err := store.PutLocked(t.Context(), "events/1.gz", []byte("other"), until); err != nil || created {
		t.Errorf("existing key: %v, %v, want false and no error", created, err)
	}
	var answered *s3Error
	if _, _, err := store.PutLocked(t.Context(), "events/1.gz", nil, until); !errors.As(err, &answered) || answered.Status != 409 {
		t.Errorf("conflict, twice: %v, want the store's own error", err)
	}
	if _, _, err := store.PutLocked(t.Context(), "events/1.gz", nil, until); !errors.As(err, &answered) || answered.Status != 307 {
		t.Errorf("redirect: %v, want it refused rather than followed", err)
	}
}

func TestS3Store_RetriesOnceAndBoundsTheAnswer(t *testing.T) {
	f := &fakeS3{t: t, answers: []func(http.ResponseWriter){
		answer(503, `<Error><Code>SlowDown</Code></Error>`),
		answer(200, `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`),
		answer(500, ""), answer(500, ""),
		answer(403, `<Error><Code>AccessDenied</Code></Error>`),
		answer(200, strings.Repeat("x", maxObjectBytes+1)),
	}}
	store := f.store()
	if enabled, err := store.LockEnabled(t.Context()); err != nil || !enabled {
		t.Errorf("after one 503: %v, %v", enabled, err)
	}
	if _, err := store.LockEnabled(t.Context()); err == nil {
		t.Error("two server errors in a row must fail")
	}
	if _, err := store.LockEnabled(t.Context()); err == nil || len(f.requests) != 5 {
		t.Errorf("a refusal is not tried again: %v after %d requests", err, len(f.requests))
	}
	if _, err := store.Get(t.Context(), "big", "v"); !errors.Is(err, errTooLarge) {
		t.Errorf("an answer past the bound: %v", err)
	}
}

func TestS3Store_Get(t *testing.T) {
	f := &fakeS3{t: t, answers: []func(http.ResponseWriter){
		func(w http.ResponseWriter) {
			w.Header().Set(headerMode, "COMPLIANCE")
			w.Header().Set(headerUntil, "2027-01-01T20:04:05Z")
			_, _ = w.Write([]byte("content"))
		},
		answer(200, "unlocked"),
	}}
	store := f.store()
	object, err := store.Get(t.Context(), "events/1.gz", "v 1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(object.Body) != "content" || object.Mode != "COMPLIANCE" || object.RetainUntil.Year() != 2027 {
		t.Errorf("object = %+v", object)
	}
	if got := f.requests[0].URL.RawQuery; got != "versionId=v%201" {
		t.Errorf("query = %q", got)
	}
	object, err = store.Get(t.Context(), "events/1.gz", "v2")
	if err != nil || object.Mode != "" || !object.RetainUntil.IsZero() {
		t.Errorf("unlocked object: %+v, %v", object, err)
	}
}

func TestS3Store_VersionsKeepsOrderAcrossPages(t *testing.T) {
	f := &fakeS3{t: t, answers: []func(http.ResponseWriter){
		answer(200, `<ListVersionsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
			<Prefix>events/</Prefix><IsTruncated>true</IsTruncated>
			<NextKeyMarker>events/1</NextKeyMarker><NextVersionIdMarker>v2</NextVersionIdMarker>
			<DeleteMarker><Key>events/1</Key><VersionId>m1</VersionId><LastModified>2026-11-25T08:58:49.000Z</LastModified></DeleteMarker>
			<Version><Key>events/1</Key><VersionId>v2</VersionId><LastModified>2026-11-25T08:58:48.000Z</LastModified></Version>
		</ListVersionsResult>`),
		answer(200, `<ListVersionsResult><IsTruncated>false</IsTruncated>
			<Version><Key>events/1</Key><VersionId>v1</VersionId><LastModified>2026-11-25T08:58:47.000Z</LastModified></Version>
		</ListVersionsResult>`),
		answer(200, `<ListVersionsResult><IsTruncated>true</IsTruncated><NextKeyMarker>events/2</NextKeyMarker></ListVersionsResult>`),
		answer(200, `<ListVersionsResult><IsTruncated>true</IsTruncated></ListVersionsResult>`),
	}}
	store := f.store()
	got, err := store.Versions(t.Context(), "events/")
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	at := func(sec int) time.Time { return time.Date(2026, 11, 25, 8, 58, sec, 0, time.UTC) }
	want := []objectVersion{{"events/1", "m1", true, at(49)}, {"events/1", "v2", false, at(48)}, {"events/1", "v1", false, at(47)}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if original := originalVersions(got)["events/1"]; original.VersionID != "v1" {
		t.Errorf("original = %v, want the oldest version, not the marker", original)
	}
	if q := f.requests[1].URL.Query(); q.Get("key-marker") != "events/1" || q.Get("version-id-marker") != "v2" {
		t.Errorf("second page asked %v", q)
	}
	// A page with no version marker continues without one: an empty one is refused.
	if _, err := store.Versions(t.Context(), "events/"); err == nil {
		t.Error("a truncated page without a marker must not read as the end")
	}
	if q := f.requests[3].URL.Query(); q.Has("version-id-marker") || q.Get("key-marker") != "events/2" {
		t.Errorf("third page asked %v", q)
	}
}
