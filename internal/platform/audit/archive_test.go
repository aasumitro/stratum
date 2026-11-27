package audit

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/testsupport"
)

const (
	archiveTestOrg = "archive_test_org"
	archivePerson  = "0f5c2f0e-archive-person"
)

// memoryStore is a bucket with Object Lock held in memory.
type memoryStore struct {
	mu       sync.Mutex
	unlocked bool
	// putErr and getErr fail the next calls; mode and until override the stored lock.
	putErr, getErr error
	mode           string
	until          time.Time
	versions       map[string][]storedVersion // newest first
	next           int
	// beforePut runs at the start of each upload, outside the store's lock.
	beforePut func()
}

type storedVersion struct {
	objectVersion
	lockedObject
}

func newMemoryStore() *memoryStore {
	return &memoryStore{mode: lockModeCompliance, versions: map[string][]storedVersion{}}
}

func (m *memoryStore) LockEnabled(context.Context) (bool, error) { return !m.unlocked, nil }

func (m *memoryStore) PutLocked(_ context.Context, key string, body []byte, until time.Time) (string, bool, error) {
	if m.beforePut != nil {
		m.beforePut()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.putErr != nil {
		return "", false, m.putErr
	}
	if len(m.versions[key]) > 0 {
		return "", false, nil
	}
	if !m.until.IsZero() {
		until = m.until
	}
	return m.write(key, body, until), true, nil
}

// write adds a version over whatever is there, as any writer to a bucket can.
func (m *memoryStore) write(key string, body []byte, until time.Time) string {
	m.next++
	id := fmt.Sprintf("v%d", m.next)
	m.versions[key] = slices.Insert(m.versions[key], 0, storedVersion{
		objectVersion{Key: key, VersionID: id, LastModified: time.Unix(int64(m.next), 0)},
		lockedObject{Body: slices.Clone(body), Mode: m.mode, RetainUntil: until},
	})
	return id
}

// mark hides key behind a delete marker, as a plain DELETE or a lifecycle rule does.
func (m *memoryStore) mark(key string) {
	m.next++
	m.versions[key] = slices.Insert(m.versions[key], 0, storedVersion{
		Key: key, VersionID: fmt.Sprintf("m%d", m.next), IsDeleteMarker: true, LastModified: time.Unix(int64(m.next), 0),
	})
}

func (m *memoryStore) Get(_ context.Context, key, versionID string) (*lockedObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	for _, v := range m.versions[key] {
		if v.VersionID == versionID && !v.IsDeleteMarker {
			object := v.lockedObject
			return &object, nil
		}
	}
	return nil, &s3Error{Status: 404, Code: "NoSuchKey"}
}

func (m *memoryStore) Versions(_ context.Context, prefix string) ([]objectVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []objectVersion
	for key, versions := range m.versions {
		if strings.HasPrefix(key, prefix) {
			for _, v := range versions {
				out = append(out, v.objectVersion)
			}
		}
	}
	return out, nil
}

// keys lists the stored objects in order.
func (m *memoryStore) keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for key := range m.versions {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

func (m *memoryStore) original(key string) *storedVersion {
	versions := m.versions[key]
	for i := len(versions) - 1; i >= 0; i-- {
		if !versions[i].IsDeleteMarker {
			return &versions[i]
		}
	}
	return nil
}

func (m *memoryStore) content(t *testing.T, key string) (archiveHeader, []archivedEvent) {
	t.Helper()
	header, events, err := decodeArchiveObject(m.original(key).Body)
	if err != nil {
		t.Fatalf("decode %s: %v", key, err)
	}
	return header, events
}

type archiveFixture struct {
	admin   *pgxpool.Pool
	worker  *pgxpool.Pool
	store   *memoryStore
	archive *Archive
	logs    *bytes.Buffer
	seeded  int
}

func archivePool(t *testing.T, env string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skip(env + " not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open %s pool: %v", env, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newArchiveFixture starts from an archive that has never run. Seeded rows are dated
// 300 days back, before every row other packages' tests write meanwhile, so a claim of
// exactly as many rows takes them and leaves the others.
func newArchiveFixture(t *testing.T) *archiveFixture {
	t.Helper()
	f := &archiveFixture{
		admin: archivePool(t, "TEST_DATABASE_URL"), worker: archivePool(t, "POSTGRES_WORKER_URL"),
		store: newMemoryStore(), logs: &bytes.Buffer{},
	}
	f.useStore(f.store)
	reset := func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM audit.events WHERE organization_id LIKE '` + archiveTestOrg + `%'`,
			`UPDATE audit.events SET export_seq = NULL WHERE export_seq IS NOT NULL`,
			`DELETE FROM audit.exports`,
		} {
			if _, err := f.admin.Exec(ctx, stmt); err != nil {
				t.Errorf("reset: %v", err)
			}
		}
	}
	reset()
	t.Cleanup(reset)
	return f
}

// useStore points the archive at store. No row counts as late, however old: other
// packages' tests leave rows that are never exported.
func (f *archiveFixture) useStore(store objectStore) {
	f.archive = newArchive(f.worker, store, slog.New(slog.NewTextHandler(f.logs, nil)), 400, 400)
	f.archive.overdue = 100 * 365 * 24 * time.Hour
}

// wantProblem fails unless the report holds a problem of kind that contains part.
func wantProblem(t *testing.T, report *VerifyReport, kind, part string) {
	t.Helper()
	if report.OK() || !slices.ContainsFunc(report.Problems(kind), func(p string) bool { return strings.Contains(p, part) }) {
		t.Errorf("problems = %v, want %s naming %q", report.problems, kind, part)
	}
}

// seededAt dates the nth seeded row.
func seededAt(n int) time.Time {
	return time.Now().UTC().AddDate(0, 0, -300).Truncate(time.Hour).Add(time.Duration(n) * time.Second)
}

// seed adds a row and returns its id.
func (f *archiveFixture) seed(t *testing.T, actor string) string {
	t.Helper()
	f.seeded++
	var id string
	if err := f.admin.QueryRow(t.Context(), `
		INSERT INTO audit.events (organization_id, actor, action, resource, status_code, metadata, ip, user_agent, created_at)
		VALUES ($1, $2, 'PATCH', '/thing', 200, '{"email": "person@example.com"}', '203.0.113.7', 'agent/1.0', $3)
		RETURNING id::text`,
		archiveTestOrg, actor, seededAt(f.seeded)).Scan(&id); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return id
}

// next runs one export step as a run would start it.
func (f *archiveFixture) next(t *testing.T) (int, bool, error) {
	t.Helper()
	bucket, err := f.archive.listBucket(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return f.archive.exportNext(t.Context(), bucket)
}

// export claims and stores exactly n rows.
func (f *archiveFixture) export(t *testing.T, n int) {
	t.Helper()
	f.archive.batch = n
	rows, _, err := f.next(t)
	if err != nil || rows != n {
		t.Fatalf("export: %d rows, %v, want %d", rows, err, n)
	}
}

// claimOnly claims one row and fails to store it.
func (f *archiveFixture) claimOnly(t *testing.T) {
	t.Helper()
	f.archive.batch = 1
	f.store.putErr = errors.New("bucket unreachable")
	if _, _, err := f.next(t); err == nil {
		t.Fatal("want the upload's error")
	}
	f.store.putErr = nil
}

// salt is the salt of export seq.
func (f *archiveFixture) salt(t *testing.T, seq int) string {
	t.Helper()
	var salt string
	if err := f.admin.QueryRow(t.Context(), `SELECT salt FROM audit.exports WHERE seq = $1`, seq).Scan(&salt); err != nil {
		t.Fatalf("salt: %v", err)
	}
	return salt
}

func (f *archiveFixture) verify(t *testing.T) *VerifyReport {
	t.Helper()
	report, err := f.archive.Verify(t.Context(), nil)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return report
}

func (f *archiveFixture) adminExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	tag, err := f.admin.Exec(t.Context(), sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("%s: changed %d rows, want 1", sql, tag.RowsAffected())
	}
}

func TestArchive_ObjectKeepsOperatorRowsAndNamesNoPerson(t *testing.T) {
	f := newArchiveFixture(t)
	f.seed(t, "operator:op_ana")
	person := f.seed(t, archivePerson)
	f.seed(t, "anonymous")
	f.seed(t, "deleted_user")
	f.export(t, 4)

	keys := f.store.keys()
	if len(keys) != 1 || !strings.HasPrefix(keys[0], "events/000000000001-") {
		t.Fatalf("stored %v", keys)
	}
	header, events := f.store.content(t, keys[0])
	if header.Seq != 1 || header.PrevSHA256 != "" || header.Count != 4 || header.RetentionDays != 400 || len(events) != 4 {
		t.Errorf("header = %+v with %d events", header, len(events))
	}
	salt := f.salt(t, 1)
	if e := events[0]; e.Actor != "operator:op_ana" || !strings.Contains(string(e.Metadata), "person@example.com") ||
		e.IP != "203.0.113.7" || e.UserAgent != "agent/1.0" {
		t.Errorf("operator row must be kept whole: %+v", e)
	}
	if e := events[2]; e.Actor != "anonymous" || e.Metadata != nil || e.IP != "203.0.113.7" || e.UserAgent != "agent/1.0" ||
		e.MetadataDigest != digest(salt, `{"email": "person@example.com"}`) {
		t.Errorf("an unauthenticated row keeps its address and client and only a digest of the body: %+v", e)
	}
	if e := events[1]; e.ID != person || e.ActorSHA256 != sha256Hex([]byte(archivePerson)) ||
		e.ClientDigest != digest(salt, "203.0.113.7", "agent/1.0") || e.Action != "PATCH" || *e.OrganizationID != archiveTestOrg {
		t.Errorf("person's row = %+v", e)
	}
	if e := events[3]; e.Actor != "deleted_user" || e.ActorSHA256 != "" {
		t.Errorf("erased person's row = %+v", e)
	}
	for _, i := range []int{1, 2, 3} {
		line, err := json.Marshal(events[i])
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		private := []string{"person@example.com", sha256Hex([]byte(`{"email": "person@example.com"}`))}
		if i != 2 {
			private = append(private, archivePerson, "203.0.113.7", "agent/1.0")
		}
		for _, word := range private {
			if bytes.Contains(line, []byte(word)) {
				t.Errorf("row %d holds %q: %s", i, word, line)
			}
		}
	}

	stored := f.store.original(keys[0])
	if stored.Mode != "COMPLIANCE" || stored.RetainUntil.Before(time.Now().AddDate(0, 0, 400).Add(-time.Minute)) {
		t.Errorf("locked %s until %s", stored.Mode, stored.RetainUntil)
	}
	if report := f.verify(t); !report.OK() || report.Matched != 4 {
		t.Errorf("a fresh archive must verify: %+v", report)
	}
}

// The row of a transaction that commits after an export is still exported, however
// early it is dated.
func TestArchive_LateCommitIsExported(t *testing.T) {
	f := newArchiveFixture(t)
	slow, err := f.admin.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = slow.Rollback(context.Background()) }()
	var late string
	if err := slow.QueryRow(t.Context(), `
		INSERT INTO audit.events (organization_id, actor, action, resource, status_code, created_at)
		VALUES ($1, 'operator:op_ana', 'late', '/late', 200, $2) RETURNING id::text`, archiveTestOrg, seededAt(-60)).Scan(&late); err != nil {
		t.Fatalf("insert: %v", err)
	}
	f.seed(t, "operator:op_ana")
	f.export(t, 1)
	if err := slow.Commit(t.Context()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	f.export(t, 1)

	keys := f.store.keys()
	first, _ := f.store.content(t, keys[0])
	second, events := f.store.content(t, keys[1])
	if len(events) != 1 || events[0].ID != late {
		t.Errorf("second object holds %+v, want the late row", events)
	}
	if want := sha256Hex(f.store.original(keys[0]).Body); first.Seq != 1 || second.Seq != 2 || second.PrevSHA256 != want {
		t.Errorf("second object names %q before it, want %q", second.PrevSHA256, want)
	}
	if report := f.verify(t); !report.OK() || report.Matched != 2 {
		t.Errorf("report = %+v", report)
	}
}

// A run goes on only while objects come back full: rows keep arriving while it runs,
// and chasing them would never end. Rows other packages' tests leave unexported make a
// short claim impossible to set up here, so only the full one is checked.
func TestArchive_AFullObjectAsksForAnother(t *testing.T) {
	f := newArchiveFixture(t)
	for range 3 {
		f.seed(t, "operator:op_ana")
	}
	f.archive.batch = 2
	if rows, more, err := f.next(t); err != nil || rows != 2 || !more {
		t.Errorf("full object: %d rows, more=%v, %v", rows, more, err)
	}
}

func TestArchive_FailedUploadIsResumedWithTheSameRows(t *testing.T) {
	f := newArchiveFixture(t)
	id := f.seed(t, "operator:op_ana")
	f.claimOnly(t)
	// A row that arrives meanwhile waits for the next number.
	f.seed(t, "operator:op_ana")
	f.export(t, 1)
	_, events := f.store.content(t, f.store.keys()[0])
	if len(events) != 1 || events[0].ID != id {
		t.Errorf("resumed object holds %+v, want only %s", events, id)
	}
	f.export(t, 1)
	if report := f.verify(t); !report.OK() || report.Matched != 2 || report.Objects != 2 {
		t.Errorf("report = %+v", report)
	}
}

// The upload succeeded and the run stopped before recording it. What is recorded is
// the object that was stored, also when something was written over it since.
func TestArchive_StoredButUnrecordedUploadIsRecorded(t *testing.T) {
	f := newArchiveFixture(t)
	f.seed(t, archivePerson)
	f.archive.batch = 1
	f.store.getErr = errors.New("connection reset")
	if _, _, err := f.next(t); err == nil {
		t.Fatal("want the read-back's error")
	}
	f.store.getErr = nil
	key := f.store.keys()[0]
	original := f.store.original(key).Body

	f.adminExec(t, `UPDATE audit.events SET actor = 'deleted_user', ip = '', user_agent = '' WHERE organization_id = $1`, archiveTestOrg)
	f.store.write(key, []byte("written over"), time.Now())
	f.export(t, 1)

	var recorded string
	if err := f.admin.QueryRow(t.Context(), `SELECT sha256 FROM audit.exports WHERE seq = 1`).Scan(&recorded); err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if recorded != sha256Hex(original) {
		t.Error("the ledger must record the original object")
	}
	report := f.verify(t)
	if report.Anonymized != 1 || len(report.Problems(problemOverwrite)) != 1 {
		t.Errorf("report = %+v", report)
	}
}

// What an earlier attempt left under the export's name must be that attempt's object.
func TestArchive_ResumeRefusesAnObjectItDidNotWrite(t *testing.T) {
	f := newArchiveFixture(t)
	f.seed(t, "operator:op_ana")
	f.claimOnly(t)
	var objectID string
	if err := f.admin.QueryRow(t.Context(), `SELECT object_id::text FROM audit.exports WHERE seq = 1`).Scan(&objectID); err != nil {
		t.Fatalf("ledger: %v", err)
	}
	key := claimedExport{seq: 1, objectID: objectID}.key()
	f.store.write(key, []byte("garbage"), time.Now().AddDate(1, 0, 0))
	if rows, _, err := f.next(t); err == nil {
		t.Errorf("stored %d rows over another object", rows)
	}
}

func TestArchive_TwoExportersStoreOneObject(t *testing.T) {
	f := newArchiveFixture(t)
	f.seed(t, "operator:op_ana")
	f.seed(t, "operator:op_ana")
	f.claimOnly(t)

	// Both resume the same claim, and both reach the upload before either finishes.
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	f.store.beforePut = func() {
		arrived <- struct{}{}
		<-release
	}
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range 2 {
		wg.Go(func() { _, _, results[i] = f.next(t) })
	}
	<-arrived
	<-arrived
	close(release)
	wg.Wait()
	for _, err := range results {
		if err != nil {
			t.Errorf("export: %v", err)
		}
	}
	keys := f.store.keys()
	if len(keys) != 1 || len(f.store.versions[keys[0]]) != 1 {
		t.Errorf("stored %v", keys)
	}
}

func TestArchive_RefusesABucketThatWouldNotHoldTheObject(t *testing.T) {
	for name, c := range map[string]struct {
		breakStore func(*memoryStore)
		want       string
	}{
		"no Object Lock":  {func(m *memoryStore) { m.unlocked = true }, ErrArchiveNotLocked.Error()},
		"governance mode": {func(m *memoryStore) { m.mode = "GOVERNANCE" }, "lock mode"},
		"shorter lock":    {func(m *memoryStore) { m.until = time.Now().AddDate(0, 0, 399) }, "locked until"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newArchiveFixture(t)
			reader := testsupport.ManualMetricReader(t)
			f.useStore(f.store)
			f.seed(t, "operator:op_ana")
			f.archive.maxBatch = 1
			c.breakStore(f.store)
			f.archive.Run(t.Context())
			if !strings.Contains(f.logs.String(), c.want) {
				t.Errorf("log %q, want %q", f.logs, c.want)
			}
			if got := testsupport.CounterTotal(t, reader, "stratum.audit.archive.failures"); got != 1 {
				t.Errorf("failures = %d, want 1", got)
			}
			var finished int
			if err := f.admin.QueryRow(t.Context(), `SELECT count(*) FROM audit.exports WHERE uploaded_at IS NOT NULL`).Scan(&finished); err != nil {
				t.Fatalf("ledger: %v", err)
			}
			if finished != 0 {
				t.Error("an object that is not held must not count as exported")
			}
		})
	}
}

// The claim's time is the database's, whose clock can be ahead of this process's.
func TestArchive_LockCoversAClaimFromAClockThatIsAhead(t *testing.T) {
	f := newArchiveFixture(t)
	f.seed(t, "operator:op_ana")
	f.claimOnly(t)
	f.adminExec(t, `UPDATE audit.exports SET claimed_at = now() + interval '3 hours'`)
	f.export(t, 1)
	if report := f.verify(t); !report.OK() {
		t.Errorf("report = %+v", report)
	}
}

// Raising the retention later leaves earlier objects as they were written.
func TestArchive_ALongerRetentionLaterKeepsEarlierObjectsValid(t *testing.T) {
	f := newArchiveFixture(t)
	f.seed(t, "operator:op_ana")
	f.export(t, 1)
	f.seed(t, "operator:op_ana")
	f.claimOnly(t)
	f.store.getErr = errors.New("connection reset")
	if _, _, err := f.next(t); err == nil {
		t.Fatal("want the read-back's error")
	}
	f.store.getErr = nil

	f.archive.retentionDays = 800
	f.export(t, 1)
	if report := f.verify(t); !report.OK() {
		t.Errorf("report = %+v", report)
	}
}

func TestArchive_ShutdownIsNotAFailureAndTheNextRunFinishes(t *testing.T) {
	f := newArchiveFixture(t)
	reader := testsupport.ManualMetricReader(t)
	f.useStore(f.store)
	f.seed(t, "operator:op_ana")
	f.archive.maxBatch = 1
	ctx, cancel := context.WithCancel(t.Context())
	f.store.beforePut = cancel
	f.archive.Run(ctx)
	if got := testsupport.CounterTotal(t, reader, "stratum.audit.archive.failures"); got != 0 || f.logs.Len() != 0 {
		t.Errorf("failures = %d, log %q", got, f.logs)
	}
	f.store.beforePut = nil
	// The claim is still open, whatever the batch asks for now.
	f.export(t, 1)
	if keys := f.store.keys(); len(keys) != 1 {
		t.Errorf("stored %v, want the one object", keys)
	}
}

func TestArchive_ClaimTriesFewerRowsWhenItRunsOutOfTime(t *testing.T) {
	f := newArchiveFixture(t)
	f.seed(t, "operator:op_ana")
	config, err := pgxpool.ParseConfig(os.Getenv("POSTGRES_WORKER_URL"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	config.ConnConfig.RuntimeParams["statement_timeout"] = "50ms"
	slow, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(slow.Close)
	// Every claim waits on the ledger's lock until its time runs out.
	holder, err := f.admin.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = holder.Rollback(context.Background()) }()
	if _, err := holder.Exec(t.Context(), `LOCK TABLE audit.exports IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock: %v", err)
	}
	f.archive.pool = slow
	f.archive.batch = 400
	_, _, err = f.archive.claim(t.Context(), 1)
	if err == nil || f.archive.batch != minArchiveBatch {
		t.Errorf("claim: %v with batch %d, want it cut to %d and then the error", err, f.archive.batch, minArchiveBatch)
	}
}

// An object waiting under the claim's name may hold rows the sweep took since, never
// rows the claim did not have.
func TestArchive_ResumeRefusesRowsTheClaimNeverHad(t *testing.T) {
	f := newArchiveFixture(t)
	real := f.seed(t, "operator:op_ana")
	f.claimOnly(t)
	var claim claimedExport
	if err := f.admin.QueryRow(t.Context(), `SELECT seq, object_id::text, salt, claimed_at FROM audit.exports`).Scan(
		&claim.seq, &claim.objectID, &claim.salt, &claim.claimedAt); err != nil {
		t.Fatalf("ledger: %v", err)
	}
	invented := archivedEvent{
		ID: "0f5c2f0e-0000-4000-8000-0000000000aa", Actor: "operator:op_victim", Action: "DELETE",
		CreatedAt: time.Now().UTC().AddDate(-2, 0, 0),
	}
	body, err := encodeArchiveObject(archiveHeader{
		Format: archiveFormat, Seq: claim.seq, Count: 2, ClaimedAt: claim.claimedAt.UTC(), RetentionDays: 400,
	}, []archivedEvent{{ID: real, CreatedAt: seededAt(1)}, invented})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	f.store.write(claim.key(), body, time.Now().AddDate(1, 0, 1))
	if rows, _, err := f.next(t); err == nil {
		t.Errorf("recorded %d rows with an invented one", rows)
	}
	// The verification reports such an object too.
	wantProblem(t, f.verify(t), problemObject, invented.ID)
}

// The verification counts an object's lock and its oldest rows from its header, so a
// resumed object must carry this claim's.
func TestArchive_ResumeRefusesAnotherClaimsHeader(t *testing.T) {
	f := newArchiveFixture(t)
	real := f.seed(t, "operator:op_ana")
	f.claimOnly(t)
	var claim claimedExport
	if err := f.admin.QueryRow(t.Context(), `SELECT seq, object_id::text, salt, claimed_at FROM audit.exports`).Scan(
		&claim.seq, &claim.objectID, &claim.salt, &claim.claimedAt); err != nil {
		t.Fatalf("ledger: %v", err)
	}
	body, err := encodeArchiveObject(archiveHeader{
		Format: archiveFormat, Seq: claim.seq, Count: 1, RetentionDays: 400, TableDays: 400,
		ClaimedAt: claim.claimedAt.UTC().AddDate(-3, 0, 0),
	}, []archivedEvent{{ID: real, CreatedAt: seededAt(1)}})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	f.store.write(claim.key(), body, time.Now().AddDate(1, 0, 1))
	if _, _, err := f.next(t); err == nil {
		t.Error("recorded an object claimed at another time")
	}
	wantProblem(t, f.verify(t), problemObject, "claimed at another time")
}

// A real export never holds a row the table no longer kept when it was claimed; such
// a row stays marked for the sweep.
func TestArchive_ARowPastRetentionAtTheClaimStaysOut(t *testing.T) {
	f := newArchiveFixture(t)
	old := f.seed(t, "operator:op_ana")
	f.adminExec(t, `UPDATE audit.events SET created_at = now() - interval '401 days' WHERE id = $1`, old)
	f.seed(t, "operator:op_ana")
	f.archive.batch = 2
	// The claim was full, so the run asks for another, however few rows went out.
	if rows, more, err := f.next(t); err != nil || rows != 1 || !more {
		t.Fatalf("export: %d rows, more=%v, %v", rows, more, err)
	}
	if report := f.verify(t); !report.OK() {
		t.Errorf("report = %+v", report)
	}
}

// Raising the table's retention after an export: once a lifecycle rule removes the
// earlier objects, their rows are still in the table and are listed, not failed.
func TestArchiveVerify_RowsOutlivingTheirCopy(t *testing.T) {
	f := newArchiveFixture(t)
	first := f.seed(t, "operator:op_ana")
	f.export(t, 1)
	f.seed(t, "operator:op_ana")
	f.export(t, 1)
	key := f.store.keys()[0]
	delete(f.store.versions, key)
	f.store.mark(key)
	f.archive.tableDays = 730
	report := f.verify(t)
	if !report.OK() || report.CopyExpired != 1 || len(report.CopyExpiredIDs) != 1 || report.CopyExpiredIDs[0] != first {
		t.Errorf("report = %+v", report)
	}
}

// A table retention changed between an upload and its record, or raised since an
// export, leaves the export as it was written.
func TestArchive_ATableRetentionChangedLaterKeepsExportsValid(t *testing.T) {
	f := newArchiveFixture(t)
	f.seed(t, "operator:op_ana")
	f.archive.batch = 1
	f.store.getErr = errors.New("connection reset")
	if _, _, err := f.next(t); err == nil {
		t.Fatal("want the read-back's error")
	}
	f.store.getErr = nil
	f.archive.tableDays = 730
	if rows, _, err := f.next(t); err != nil || rows != 1 {
		t.Fatalf("resume: %d rows, %v", rows, err)
	}
	if report := f.verify(t); !report.OK() {
		t.Errorf("report = %+v", report)
	}
}

func TestArchive_ResumeAcceptsAnObjectWhoseRowsTheSweepRemovedSince(t *testing.T) {
	f := newArchiveFixture(t)
	f.seed(t, "operator:op_ana")
	swept := f.seed(t, "operator:op_ana")
	// The second row is about to pass the table's retention.
	f.adminExec(t, `UPDATE audit.events SET created_at = now() - interval '400 days' + interval '1 minute' WHERE id = $1`, swept)
	f.archive.batch = 2
	f.store.getErr = errors.New("connection reset")
	if _, _, err := f.next(t); err == nil {
		t.Fatal("want the read-back's error")
	}
	f.store.getErr = nil
	f.adminExec(t, `DELETE FROM audit.events WHERE id = $1`, swept)
	f.archive.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if rows, _, err := f.next(t); err != nil || rows != 1 {
		t.Errorf("resume: %d rows, %v", rows, err)
	}
}

// What a stranger wrote at the bucket's next number must not stop the exporter.
func TestArchive_AnUnreadablePreviousObjectDoesNotStopTheExport(t *testing.T) {
	f := newArchiveFixture(t)
	f.seed(t, "operator:op_ana")
	f.export(t, 1)
	f.seed(t, "operator:op_ana")
	// Reading the previous object fails the way a stranger's oversized one does.
	f.archive.store = &failingPrevious{memoryStore: f.store, key: f.store.keys()[0], err: errTooLarge}
	f.export(t, 1)
	if header, events := f.store.content(t, f.store.keys()[1]); len(events) != 1 || header.PrevSHA256 != "" {
		t.Errorf("second object: %+v with %d events", header, len(events))
	}
}

// failingPrevious refuses to read one object with err.
type failingPrevious struct {
	*memoryStore
	key string
	err error
}

func (f *failingPrevious) Get(ctx context.Context, key, versionID string) (*lockedObject, error) {
	if key == f.key {
		return nil, f.err
	}
	return f.memoryStore.Get(ctx, key, versionID)
}

// Any other failure to read the previous object stops the run: an object that names
// nothing before it would read as a broken chain for its whole lock.
func TestArchive_APreviousObjectThatCannotBeReadNowStopsTheRun(t *testing.T) {
	f := newArchiveFixture(t)
	f.seed(t, "operator:op_ana")
	f.export(t, 1)
	f.seed(t, "operator:op_ana")
	f.archive.store = &failingPrevious{memoryStore: f.store, key: f.store.keys()[0], err: &s3Error{Status: 503, Code: "SlowDown"}}
	f.archive.batch = 1
	if _, _, err := f.next(t); err == nil {
		t.Error("want the run stopped")
	}
}

// What was looked into can be listed as known: a gap, and an object a stranger put
// there, which then counts for nothing else.
func TestArchiveVerify_KnownGapsAndObjects(t *testing.T) {
	f := newArchiveFixture(t)
	for range 3 {
		f.seed(t, "operator:op_ana")
		f.export(t, 1)
	}
	delete(f.store.versions, f.store.keys()[1])
	planted := claimedExport{seq: 999_999_999_999, objectID: "0f5c2f0e-0000-4000-8000-000000000001"}.key()
	header, events := f.store.content(t, f.store.keys()[0])
	body, err := encodeArchiveObject(header, events)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	f.store.write(planted, body, time.Now().AddDate(1, 0, 1))
	wantProblem(t, f.verify(t), problemChain, "numbers 2-2")

	repeat := claimedExport{seq: 1, objectID: "0f5c2f0e-0000-4000-8000-000000000002"}.key()
	f.store.write(repeat, body, time.Now().AddDate(1, 0, 1))
	known, err := ReadKnownIDs(strings.NewReader("numbers 2-2\nnumbers 4-999999999998\n" + planted + "\n" + repeat + "\n"))
	if err != nil {
		t.Fatalf("known: %v", err)
	}
	report, err := f.archive.Verify(t.Context(), known)
	// The rows of the lost second object are still marked with it.
	if err != nil || len(report.Problems(problemChain)) != 0 || len(report.Problems(problemObject)) != 0 {
		t.Errorf("report = %+v, %v", report, err)
	}
}

// A number far beyond the database's, from whoever wrote it, does not move the
// numbering there.
func TestArchive_ClaimIgnoresANumberNoRestoreExplains(t *testing.T) {
	f := newArchiveFixture(t)
	f.seed(t, "operator:op_ana")
	var seq int64
	if err := f.worker.QueryRow(t.Context(), `SELECT seq FROM audit.claim_export(1, 999999999999)`).Scan(&seq); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if seq != 1 {
		t.Errorf("claimed %d, want 1", seq)
	}
	if got, ok := archiveSeq("events/1000000000000-0f5c2f0e-0000-4000-8000-000000000001.ndjson.gz"); !ok || got != 1_000_000_000_000 {
		t.Errorf("a 13-digit number reads as %d, %v", got, ok)
	}
}

func TestArchive_PendingCountsUnclaimedAndUnfinishedRows(t *testing.T) {
	f := newArchiveFixture(t)
	count := func() int64 {
		t.Helper()
		n, err := f.archive.Pending(t.Context())
		if err != nil {
			t.Fatalf("pending: %v", err)
		}
		return n
	}
	// Only the seeded row is this old, so rows other packages' tests write do not count.
	f.archive.overdue = 200 * 24 * time.Hour
	if got := count(); got != 0 {
		t.Fatalf("precondition: %d rows late", got)
	}
	f.seed(t, "operator:op_ana")
	if got := count(); got != 1 {
		t.Errorf("an old unexported row adds %d, want 1", got)
	}
	if report := f.verify(t); report.OK() || report.Waiting != 1 {
		t.Errorf("an archive that has not run must not verify: %+v", report)
	}
	future := f.seed(t, "operator:op_ana")
	f.adminExec(t, `UPDATE audit.events SET created_at = now() + interval '201 days' WHERE id = $1`, future)
	if got := count(); got != 2 {
		t.Errorf("a row dated in the future adds %d, want 1", got-1)
	}
	f.adminExec(t, `DELETE FROM audit.events WHERE id = $1`, future)
	f.claimOnly(t)
	if got := count(); got != 0 {
		t.Errorf("a fresh claim adds %d, want 0", got)
	}
	f.adminExec(t, `UPDATE audit.exports SET claimed_at = now() - interval '201 days'`)
	if got := count(); got != 1 {
		t.Errorf("a claim that never finished adds %d, want 1", got)
	}
	f.export(t, 1)
	if report := f.verify(t); report.Waiting != 0 || !report.OK() {
		t.Errorf("report = %+v", report)
	}
}

func TestArchiveVerify_Rows(t *testing.T) {
	for name, c := range map[string]struct {
		actor  string
		change string // run by the table's owner against the one row
		kind   string
		names  string
	}{
		"operator actor":       {"operator:op_ana", `UPDATE audit.events SET actor = 'operator:op_budi' WHERE id = $1`, problemAltered, "actor"},
		"operator erased":      {"operator:op_ana", `UPDATE audit.events SET actor = 'deleted_user' WHERE id = $1`, problemAltered, "actor"},
		"operator address":     {"operator:op_ana", `UPDATE audit.events SET ip = '' WHERE id = $1`, problemAltered, "ip"},
		"operator client":      {"operator:op_ana", `UPDATE audit.events SET user_agent = '' WHERE id = $1`, problemAltered, "user_agent"},
		"anonymous body":       {"anonymous", `UPDATE audit.events SET metadata = '{}' WHERE id = $1`, problemAltered, "metadata"},
		"anonymous address":    {"anonymous", `UPDATE audit.events SET ip = '' WHERE id = $1`, problemAltered, "ip"},
		"person actor":         {archivePerson, `UPDATE audit.events SET actor = 'someone else' WHERE id = $1`, problemAltered, "actor"},
		"person to operator":   {archivePerson, `UPDATE audit.events SET actor = 'operator:op_ana' WHERE id = $1`, problemAltered, "actor"},
		"person's address":     {archivePerson, `UPDATE audit.events SET ip = '198.51.100.1' WHERE id = $1`, problemAltered, "ip"},
		"person's client":      {archivePerson, `UPDATE audit.events SET user_agent = 'other' WHERE id = $1`, problemAltered, "user_agent"},
		"metadata":             {archivePerson, `UPDATE audit.events SET metadata = '{}' WHERE id = $1`, problemAltered, "metadata"},
		"action":               {archivePerson, `UPDATE audit.events SET action = 'GET' WHERE id = $1`, problemAltered, "action"},
		"resource":             {archivePerson, `UPDATE audit.events SET resource = '/other' WHERE id = $1`, problemAltered, "resource"},
		"status":               {archivePerson, `UPDATE audit.events SET status_code = 403 WHERE id = $1`, problemAltered, "status_code"},
		"time":                 {archivePerson, `UPDATE audit.events SET created_at = created_at + interval '1 microsecond' WHERE id = $1`, problemAltered, "created_at"},
		"organization":         {archivePerson, `UPDATE audit.events SET organization_id = organization_id || 'x' WHERE id = $1`, problemAltered, "organization_id"},
		"erased and altered":   {archivePerson, `UPDATE audit.events SET actor = 'deleted_user', ip = '', user_agent = '', action = 'GET' WHERE id = $1`, problemAltered, "action"},
		"erased, address kept": {archivePerson, `UPDATE audit.events SET actor = 'deleted_user' WHERE id = $1`, problemAltered, "ip"},
		"deleted row":          {archivePerson, `DELETE FROM audit.events WHERE id = $1`, problemMissing, ""},
		"added to an export": {archivePerson, `
			INSERT INTO audit.events (organization_id, actor, action, resource, status_code, export_seq)
			SELECT organization_id, 'x', 'x', 'x', 200, export_seq FROM audit.events WHERE id = $1`, problemUnstored, "not in"},
		"marked with no export": {archivePerson, `
			INSERT INTO audit.events (organization_id, actor, action, resource, status_code, export_seq)
			SELECT organization_id, 'x', 'x', 'x', 200, 99 FROM audit.events WHERE id = $1`, problemUnstored, "no object"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newArchiveFixture(t)
			id := f.seed(t, c.actor)
			f.export(t, 1)
			f.adminExec(t, c.change, id)
			wantProblem(t, f.verify(t), c.kind, c.names)
		})
	}
}

// An account whose deletion has anonymized its rows and not yet removed the account is
// not tampering.
func TestArchiveVerify_AnonymizedWhileTheDeletionIsUnderWay(t *testing.T) {
	f := newArchiveFixture(t)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = f.admin.Exec(ctx, `DELETE FROM account.tasks WHERE auth_sub = $1`, archivePerson)
		_, _ = f.admin.Exec(ctx, `DELETE FROM account.users WHERE auth_sub = $1`, archivePerson)
	})
	f.adminExec(t, `INSERT INTO account.users (auth_sub, email) VALUES ($1, 'archive-leaving@example.com')`, archivePerson)
	f.adminExec(t, `INSERT INTO account.tasks (auth_sub, kind, status) VALUES ($1, 'delete_account', 'processing')`, archivePerson)
	f.seed(t, archivePerson)
	f.export(t, 1)
	if err := AnonymizeActor(t.Context(), f.worker, archivePerson); err != nil {
		t.Fatalf("anonymize: %v", err)
	}
	if report := f.verify(t); !report.OK() || len(report.Deleting) != 1 {
		t.Errorf("report = %+v", report)
	}

	// A task that failed, or that has been open for days, is not a deletion under way.
	for _, task := range []string{
		`UPDATE account.tasks SET status = 'failed' WHERE auth_sub = $1`,
		`UPDATE account.tasks SET status = 'processing', created_at = now() - interval '4 days' WHERE auth_sub = $1`,
	} {
		f.adminExec(t, task, archivePerson)
		wantProblem(t, f.verify(t), problemAltered, "the account still exists")
	}
}

// A living person's row made to look erased hides who acted.
func TestArchiveVerify_AnonymizedWhileTheAccountExists(t *testing.T) {
	f := newArchiveFixture(t)
	t.Cleanup(func() {
		_, _ = f.admin.Exec(context.Background(), `DELETE FROM account.users WHERE auth_sub = $1`, archivePerson)
	})
	f.adminExec(t, `INSERT INTO account.users (auth_sub, email) VALUES ($1, 'archive-living@example.com')`, archivePerson)
	f.seed(t, archivePerson)
	f.export(t, 1)
	if err := AnonymizeActor(t.Context(), f.worker, archivePerson); err != nil {
		t.Fatalf("anonymize: %v", err)
	}
	wantProblem(t, f.verify(t), problemAltered, "the account still exists")
}

func TestArchiveVerify_LegitimateChangesPass(t *testing.T) {
	t.Run("account deletion", func(t *testing.T) {
		f := newArchiveFixture(t)
		f.seed(t, archivePerson)
		f.seed(t, "operator:op_ana")
		f.export(t, 2)
		if err := AnonymizeActor(t.Context(), f.worker, archivePerson); err != nil {
			t.Fatalf("anonymize: %v", err)
		}
		if report := f.verify(t); !report.OK() || report.Anonymized != 1 || report.Matched != 1 {
			t.Errorf("report = %+v", report)
		}
	})
	t.Run("the table's own retention", func(t *testing.T) {
		f := newArchiveFixture(t)
		id := f.seed(t, archivePerson)
		f.export(t, 1)
		f.adminExec(t, `DELETE FROM audit.events WHERE id = $1`, id)
		// A table that keeps rows a day, with an export claimed when the row was young.
		key := f.store.keys()[0]
		header, events := f.store.content(t, key)
		header.ClaimedAt = events[0].CreatedAt
		body, err := encodeArchiveObject(header, events)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		f.store.original(key).Body = body
		f.adminExec(t, `UPDATE audit.exports SET claimed_at = $1 WHERE seq = 1`, header.ClaimedAt)
		f.archive.tableDays = 1
		if report := f.verify(t); !report.OK() || report.Expired != 1 {
			t.Errorf("report = %+v", report)
		}
	})
	t.Run("rows known to be gone", func(t *testing.T) {
		f := newArchiveFixture(t)
		id := f.seed(t, archivePerson)
		f.export(t, 1)
		f.adminExec(t, `DELETE FROM audit.events WHERE id = $1`, id)
		known, err := ReadKnownIDs(strings.NewReader("# restored 2026-11-26\n\n" + id + "\n"))
		if err != nil {
			t.Fatalf("known: %v", err)
		}
		report, err := f.archive.Verify(t.Context(), known)
		if err != nil || !report.OK() || report.Known != 1 {
			t.Errorf("report = %+v, %v", report, err)
		}
		if _, err := ReadKnownIDs(strings.NewReader("not-an-id\n")); err == nil {
			t.Error("want a line that is not an id refused")
		}
		if known, err := ReadKnownIDs(strings.NewReader(f.store.keys()[0] + "\n")); err != nil || !known[f.store.keys()[0]] {
			t.Errorf("an object name: %v, %v", known, err)
		}
	})
	t.Run("objects past their lock, removed by a lifecycle rule", func(t *testing.T) {
		f := newArchiveFixture(t)
		for range 3 {
			f.seed(t, "operator:op_ana")
			f.export(t, 1)
		}
		keys := f.store.keys()
		// Expiry marks the first object deleted; a day later its version goes too. Its
		// rows are past the table's retention by then.
		first := f.store.original(keys[0])
		header, events := f.store.content(t, keys[0])
		header.ClaimedAt = time.Now().UTC().AddDate(-2, 0, 0).Truncate(time.Microsecond)
		f.adminExec(t, `UPDATE audit.exports SET claimed_at = $1 WHERE seq = 1`, header.ClaimedAt)
		body, err := encodeArchiveObject(header, events)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		first.Body, first.RetainUntil = body, header.ClaimedAt.AddDate(0, 0, 400).Add(time.Second)
		// The objects after it name its new hash, as they would have named the real one.
		prev := sha256Hex(body)
		for _, key := range keys[1:] {
			header, events := f.store.content(t, key)
			header.PrevSHA256, header.ClaimedAt = prev, time.Now().UTC().AddDate(-2, 0, 0).Truncate(time.Microsecond)
			f.adminExec(t, `UPDATE audit.exports SET claimed_at = $1 WHERE seq = $2`, header.ClaimedAt, header.Seq)
			body, err := encodeArchiveObject(header, events)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			f.store.original(key).Body, prev = body, sha256Hex(body)
		}
		f.store.mark(keys[0])
		f.archive.tableDays = 1
		if report := f.verify(t); !report.OK() || report.Objects != 3 {
			t.Errorf("marked: %+v", report)
		}
		delete(f.store.versions, keys[0])
		f.store.mark(keys[0])
		if report := f.verify(t); !report.OK() || report.Objects != 2 {
			t.Errorf("removed: %+v", report)
		}
	})
}

func TestArchiveVerify_Objects(t *testing.T) {
	for name, c := range map[string]struct {
		change func(t *testing.T, f *archiveFixture, keys []string)
		kind   string
	}{
		"written over": {func(t *testing.T, f *archiveFixture, keys []string) {
			f.store.write(keys[0], []byte("x"), time.Now())
		}, problemOverwrite},
		"hidden by a marker while locked": {func(t *testing.T, f *archiveFixture, keys []string) {
			f.store.mark(keys[0])
		}, problemOverwrite},
		"not locked": {func(t *testing.T, f *archiveFixture, keys []string) {
			f.store.original(keys[0]).Mode = ""
		}, problemLock},
		"gone from the middle": {func(t *testing.T, f *archiveFixture, keys []string) {
			delete(f.store.versions, keys[1])
		}, problemChain},
		"gone from the middle with its record": {func(t *testing.T, f *archiveFixture, keys []string) {
			delete(f.store.versions, keys[1])
			f.adminExec(t, `DELETE FROM audit.exports WHERE seq = 2`)
		}, problemChain},
		"record removed": {func(t *testing.T, f *archiveFixture, keys []string) {
			f.adminExec(t, `DELETE FROM audit.exports WHERE seq = 3`)
		}, problemObject},
		"record pointed elsewhere": {func(t *testing.T, f *archiveFixture, keys []string) {
			f.adminExec(t, `UPDATE audit.exports SET object_id = gen_random_uuid(), retain_until = now() - interval '1 day' WHERE seq = 1`)
		}, problemObject},
		"does not follow": {func(t *testing.T, f *archiveFixture, keys []string) {
			header, events := f.store.content(t, keys[2])
			header.PrevSHA256 = "other"
			body, err := encodeArchiveObject(header, events)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			f.store.original(keys[2]).Body = body
		}, problemChain},
		"unreadable": {func(t *testing.T, f *archiveFixture, keys []string) {
			f.store.original(keys[2]).Body = []byte("x")
		}, problemObject},
		"another number inside": {func(t *testing.T, f *archiveFixture, keys []string) {
			header, events := f.store.content(t, keys[2])
			header.Seq = 9
			body, err := encodeArchiveObject(header, events)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			f.store.original(keys[2]).Body = body
		}, problemObject},
		"an id the table never writes": {func(t *testing.T, f *archiveFixture, keys []string) {
			header, events := f.store.content(t, keys[2])
			events[0].ID = "not-a-uuid"
			body, err := encodeArchiveObject(header, events)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			f.store.original(keys[2]).Body = body
		}, problemObject},
		"a stranger's file": {func(t *testing.T, f *archiveFixture, keys []string) {
			f.store.write("events/notes.txt", []byte("x"), time.Now())
		}, problemObject},
		"a second object under a number": {func(t *testing.T, f *archiveFixture, keys []string) {
			header, events := f.store.content(t, keys[0])
			body, err := encodeArchiveObject(header, events)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			f.store.write(claimedExport{seq: 1, objectID: "00000000-0000-4000-8000-000000000000"}.key(), body, time.Now().AddDate(1, 0, 0))
		}, problemObject},
	} {
		t.Run(name, func(t *testing.T) {
			f := newArchiveFixture(t)
			for range 3 {
				f.seed(t, "operator:op_ana")
				f.export(t, 1)
			}
			c.change(t, f, f.store.keys())
			report := f.verify(t)
			if report.OK() || len(report.Problems(c.kind)) == 0 {
				t.Errorf("problems = %v, want %s", report.problems, c.kind)
			}
		})
	}
}

// The schema owner can claim rows by hand and keep the claim fresh. Only the export
// right after the bucket's highest number counts as in flight, and not while nothing
// has reached the bucket for longer than a run's interval.
func TestArchiveVerify_AnExportKeptInFlightByHand(t *testing.T) {
	f := newArchiveFixture(t)
	f.seed(t, "operator:op_ana")
	f.export(t, 1)
	hidden := f.seed(t, "operator:op_ana")
	f.adminExec(t, `INSERT INTO audit.exports (seq) VALUES (2)`)
	f.adminExec(t, `UPDATE audit.events SET export_seq = 2 WHERE id = $1`, hidden)

	if report := f.verify(t); !report.OK() {
		t.Fatalf("an export claimed just now is in flight: %+v", report)
	}
	f.store.original(f.store.keys()[0]).LastModified = time.Now().Add(-4 * time.Hour)
	// Lateness on, as a real run has it; other packages' rows are then late too, which
	// this test does not look at.
	f.archive.overdue = archiveOverdue
	wantProblem(t, f.verify(t), problemChain, "in flight")

	f.adminExec(t, `UPDATE audit.exports SET seq = 5, claimed_at = now() - interval '3 hours' WHERE seq = 2`)
	f.adminExec(t, `UPDATE audit.events SET export_seq = 5 WHERE id = $1`, hidden)
	wantProblem(t, f.verify(t), problemUnstored, "no object")

	// Writes the bucket allows anyone with its key do not keep the stall hidden: a
	// lone marker, or a version over an object whose lock has ended.
	f.adminExec(t, `UPDATE audit.exports SET seq = 2, claimed_at = now() WHERE seq = 5`)
	f.adminExec(t, `UPDATE audit.events SET export_seq = 2 WHERE id = $1`, hidden)
	f.store.mark("events/999999999999-0f5c2f0e-0000-4000-8000-000000000001.ndjson.gz")
	f.store.original(f.store.keys()[0]).RetainUntil = time.Now().Add(-time.Hour)
	f.store.write(f.store.keys()[0], []byte("x"), time.Now())
	wantProblem(t, f.verify(t), problemChain, "in flight")
}

// A run catching up stores several objects while the bucket is being listed.
func TestArchiveVerify_ExportsFinishedDuringTheListingAreExcused(t *testing.T) {
	f := newArchiveFixture(t)
	f.seed(t, "operator:op_ana")
	f.export(t, 1)
	keys := f.store.keys()
	for range 2 {
		f.seed(t, "operator:op_ana")
		f.export(t, 1)
	}
	// The listing saw only the first object.
	for _, key := range f.store.keys() {
		if key != keys[0] {
			delete(f.store.versions, key)
		}
	}
	if report := f.verify(t); !report.OK() {
		t.Errorf("report = %+v", report)
	}
}

// A stolen worker login can claim rows and record them as stored. The bucket does not
// have them, and the database's word for it counts for nothing.
func TestArchiveVerify_AnExportRecordedWithoutAnObject(t *testing.T) {
	f := newArchiveFixture(t)
	f.seed(t, "operator:op_ana")
	var seq int64
	if err := f.worker.QueryRow(t.Context(), `SELECT seq FROM audit.claim_export(1, 1)`).Scan(&seq); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := f.worker.Exec(t.Context(), `SELECT audit.finish_export($1, 'x', '2000-01-01')`, seq); err == nil {
		t.Error("an export locked for less than the floor must be refused")
	}
	if _, err := f.worker.Exec(t.Context(), `SELECT audit.finish_export($1, 'x', now() + interval '400 days')`, seq); err != nil {
		t.Fatalf("finish: %v", err)
	}
	f.adminExec(t, `UPDATE audit.exports SET uploaded_at = now() - interval '1 day'`)
	wantProblem(t, f.verify(t), problemUnstored, "no object")
}

// A database restored from an older backup numbers new objects above the bucket's.
func TestArchive_NewObjectsAfterARestoreFollowTheBucket(t *testing.T) {
	f := newArchiveFixture(t)
	f.seed(t, "operator:op_ana")
	f.export(t, 1)
	f.adminExec(t, `DELETE FROM audit.exports`)
	f.adminExec(t, `UPDATE audit.events SET export_seq = NULL WHERE organization_id = $1`, archiveTestOrg)
	f.seed(t, "operator:op_ana")
	f.export(t, 2)

	keys := f.store.keys()
	if len(keys) != 2 || !strings.HasPrefix(keys[1], "events/000000000002-") {
		t.Fatalf("stored %v", keys)
	}
	report := f.verify(t)
	// The ledger lost the first object's record with the restore.
	if report.Matched != 3 || len(report.Problems(problemChain)) != 0 || len(report.Problems(problemObject)) != 1 {
		t.Errorf("report = %+v", report)
	}
	report, err := f.archive.Verify(t.Context(), map[string]bool{keys[0]: true})
	if err != nil || !report.OK() {
		t.Errorf("with the lost record known: %+v, %v", report, err)
	}
}

func TestVerifyReport_WriteBoundsWhatItLists(t *testing.T) {
	report := &VerifyReport{problems: map[string][]string{}}
	for i := range maxListedProblems + 3 {
		report.addf(problemMissing, "row %d", i)
	}
	var out bytes.Buffer
	report.Write(&out)
	if got := strings.Count(out.String(), "row "); got != maxListedProblems {
		t.Errorf("listed %d", got)
	}
	for _, want := range []string{"missing: 53", "and 3 more", "RESULT: FAIL"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("printed %q, want %q", out.String(), want)
		}
	}
}

func TestArchive_OnlyTheWorkerExportsAndOnlyThroughTheFunctions(t *testing.T) {
	f := newArchiveFixture(t)
	f.seed(t, "operator:op_ana")
	f.export(t, 1)
	refused := func(pool *pgxpool.Pool, role, sql string) {
		t.Helper()
		_, err := pool.Exec(t.Context(), sql)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("%s: %s: %v, want insufficient privilege", role, sql, err)
		}
	}
	for _, sql := range []string{
		`INSERT INTO account.tasks (auth_sub, kind, status) VALUES ('x', 'delete_account', 'processing')`,
		`UPDATE account.tasks SET kind = 'delete_account' WHERE false`,
		`UPDATE account.tasks SET auth_sub = 'x' WHERE false`,
		`UPDATE account.tasks SET created_at = now() WHERE false`,
	} {
		refused(f.worker, "POSTGRES_WORKER_URL", sql)
	}
	if _, err := f.worker.Exec(t.Context(),
		`UPDATE account.tasks SET status = 'failed', result = NULL, error = 'x', completed_at = now() WHERE false`); err != nil {
		t.Errorf("the worker must still record how a task went: %v", err)
	}
	writes := []string{
		`INSERT INTO audit.exports (seq) VALUES (99)`,
		`UPDATE audit.exports SET sha256 = 'x'`,
		`DELETE FROM audit.exports`,
		`TRUNCATE audit.exports`,
		`UPDATE audit.events SET export_seq = NULL WHERE false`,
		`INSERT INTO audit.events (actor, action, resource, status_code, export_seq) VALUES ('x', 'x', 'x', 200, 1)`,
		`INSERT INTO audit.events (actor, action, resource, status_code, created_at) VALUES ('x', 'x', 'x', 200, '2000-01-01')`,
	}
	for _, env := range []string{"POSTGRES_APP_URL", "POSTGRES_WEBHOOK_URL", "POSTGRES_UNSCOPED_URL", "POSTGRES_OPERATOR_URL"} {
		pool := archivePool(t, env)
		for _, sql := range append([]string{
			`SELECT * FROM audit.claim_export(1, 1)`,
			`SELECT audit.finish_export(1, 'x', now())`,
			`SELECT * FROM audit.exports`,
		}, writes...) {
			refused(pool, env, sql)
		}
	}
	for _, sql := range writes {
		refused(f.worker, "POSTGRES_WORKER_URL", sql)
	}

	// Recording an export again changes nothing.
	var recorded bool
	if err := f.worker.QueryRow(t.Context(), `SELECT audit.finish_export(1, 'x', now() + interval '400 days')`).Scan(&recorded); err != nil || !recorded {
		t.Fatalf("finish again: %v, %v", recorded, err)
	}
	if report := f.verify(t); !report.OK() {
		t.Errorf("report = %+v", report)
	}
}

func TestDecodeArchiveObject_RefusesWhatItCannotTrust(t *testing.T) {
	header := archiveHeader{Format: archiveFormat, Seq: 1, Count: 1}
	good, err := encodeArchiveObject(header, []archivedEvent{{ID: "0f5c2f0e-0000-4000-8000-000000000001"}})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, events, err := decodeArchiveObject(good); err != nil || len(events) != 1 {
		t.Fatalf("round trip: %v, %v", events, err)
	}
	header.Count = archiveBatch + 1
	tooMany, _ := encodeArchiveObject(header, nil)
	header.Count = 0
	extra, _ := encodeArchiveObject(header, []archivedEvent{{ID: "0f5c2f0e-0000-4000-8000-000000000001"}})
	defer func(limit int64) { maxDecodedBytes = limit }(maxDecodedBytes)
	maxDecodedBytes = 1 << 20
	var bomb bytes.Buffer
	zip := gzip.NewWriter(&bomb)
	_, _ = zip.Write([]byte(`{"format":1,"seq":1,"count":1}` + "\n" + `{"id":"`))
	_, _ = zip.Write(bytes.Repeat([]byte("a"), int(maxDecodedBytes)))
	_ = zip.Close()
	for name, body := range map[string][]byte{
		"empty": nil, "not compressed": []byte("{}"), "cut short": good[:len(good)-9],
		"count past an object": tooMany, "more than the count": extra, "past the bound": bomb.Bytes(),
	} {
		if _, _, err := decodeArchiveObject(body); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
