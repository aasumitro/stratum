package audit

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/metric"

	"github.com/aasumitro/stratum/internal/platform/otel"
)

const (
	archivePrefix = "events/"
	archiveFormat = 1
	// archiveBatch is the most rows one object holds.
	archiveBatch = 5000
	// minArchiveBatch is as far as a claim that runs out of time is cut down.
	minArchiveBatch = 100

	// archiveOverdue is how long a row may wait for its export before it counts as
	// late: two ticks, so one failed or slow run is not an alarm.
	archiveOverdue = 2 * time.Hour
	// pendingCap bounds the count behind the gauge. Past it the number only has to be
	// large.
	pendingCap = 100_000

	// minTableDays is the shortest the table can keep a row: the database refuses less.
	minTableDays = 365

	operatorActorPrefix = "operator:"
	deletedActor        = "deleted_user"

	sqlStateQueryCanceled = "57014"
)

// archiveKeyPattern is the only name the archive writes: a number of at least 12 digits,
// then the export's id.
var archiveKeyPattern = regexp.MustCompile(
	`^` + archivePrefix + `([0-9]{12,19})-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.ndjson\.gz$`)

// maxDecodedBytes bounds an object once decompressed. A variable only so a test can
// reach it without decompressing that much.
var maxDecodedBytes int64 = 256 << 20

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ErrArchiveNotLocked means the bucket would let a stored object be changed, so
// nothing is exported to it.
var ErrArchiveNotLocked = errors.New("audit archive: the bucket has no Object Lock")

// Archive copies audit.events to a bucket where a stored object cannot be changed or
// deleted before its retention ends, by anyone, the owner of the database included.
type Archive struct {
	pool          *pgxpool.Pool
	store         objectStore
	log           *slog.Logger
	retentionDays int
	// tableDays is how long the table keeps a row: an older one is expected to be gone.
	tableDays int
	// maxBatch is what each run starts from; batch is cut down when a claim runs out of
	// time, for the rest of that run.
	maxBatch int
	batch    int
	overdue  time.Duration
	now      func() time.Time
	failures metric.Int64Counter
}

// NewArchive builds an Archive over pool, which must be the worker role's.
// retentionDays is how long an object stays locked, tableDays how long the table
// itself keeps a row.
func NewArchive(
	pool *pgxpool.Pool, cfg ArchiveStoreConfig, log *slog.Logger, retentionDays, tableDays int,
) *Archive {
	return newArchive(pool, newS3Store(cfg), log, retentionDays, tableDays)
}

func newArchive(pool *pgxpool.Pool, store objectStore, log *slog.Logger, retentionDays, tableDays int) *Archive {
	return &Archive{
		pool: pool, store: store, log: log,
		retentionDays: retentionDays, tableDays: tableDays,
		maxBatch: archiveBatch, batch: archiveBatch, overdue: archiveOverdue, now: time.Now,
		failures: otel.Counter("stratum.audit.archive.failures", "Audit archive runs that failed and will retry on the next tick."),
	}
}

// CheckLock reports ErrArchiveNotLocked for a bucket without Object Lock. Any other
// error means the bucket could not be asked.
func (a *Archive) CheckLock(ctx context.Context) error {
	enabled, err := a.store.LockEnabled(ctx)
	if err != nil {
		return fmt.Errorf("audit.Archive.CheckLock: %w", err)
	}
	if !enabled {
		return ErrArchiveNotLocked
	}
	return nil
}

// Run exports what is waiting. A failure is counted and logged, and the next run
// continues from the same rows. Shutdown is not a failure.
func (a *Archive) Run(ctx context.Context) {
	rows, err := a.export(ctx)
	if rows > 0 {
		a.log.Info("audit archive stored events", "rows", rows)
	}
	if err != nil && ctx.Err() == nil {
		a.failures.Add(ctx, 1)
		a.log.Warn("audit archive run failed", "error", err)
	}
}

// export stores objects until a claim comes back less than full. Rows keep arriving
// while it runs, so waiting for none would never end, and would lock away a stream of
// tiny objects.
func (a *Archive) export(ctx context.Context) (int, error) {
	if err := a.CheckLock(ctx); err != nil {
		return 0, err
	}
	a.batch = a.maxBatch
	bucket, err := a.listBucket(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for {
		rows, more, err := a.exportNext(ctx, bucket)
		total += rows
		if err != nil || !more {
			return total, err
		}
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
}

// bucketState is what the exporter needs to know of the objects already stored.
type bucketState struct {
	maxSeq int64
	// originals holds the first version of each object, by number.
	originals map[int64]objectVersion
	// hashes holds the hash of objects this run stored or read.
	hashes map[int64]string
}

func (a *Archive) listBucket(ctx context.Context) (*bucketState, error) {
	versions, err := a.store.Versions(ctx, archivePrefix)
	if err != nil {
		return nil, fmt.Errorf("audit.Archive: %w", err)
	}
	state := &bucketState{originals: map[int64]objectVersion{}, hashes: map[int64]string{}}
	for key, original := range originalVersions(versions) {
		if seq, ok := archiveSeq(key); ok {
			state.originals[seq] = original
			state.maxSeq = max(state.maxSeq, seq)
		}
	}
	return state, nil
}

// originalVersions picks the first version written under each key, leaving out keys
// that hold nothing but delete markers. Whatever came after it was written over it.
func originalVersions(versions []objectVersion) map[string]objectVersion {
	originals := map[string]objectVersion{}
	for _, version := range versions {
		if version.IsDeleteMarker {
			continue
		}
		// The listing is newest first, so on a tie the later one listed is older.
		if current, seen := originals[version.Key]; !seen || !version.LastModified.After(current.LastModified) {
			originals[version.Key] = version
		}
	}
	return originals
}

// claimedExport is a set of rows that has a number and no finished object yet.
type claimedExport struct {
	seq       int64
	objectID  string
	salt      string
	claimedAt time.Time
}

func (c claimedExport) key() string {
	return fmt.Sprintf("%s%012d-%s.ndjson.gz", archivePrefix, c.seq, c.objectID)
}

// claim takes the next export. A claim that runs past the statement timeout, on a
// table whose indexes make the marking slow, is tried again with half as many rows.
func (a *Archive) claim(ctx context.Context, minSeq int64) (claimedExport, bool, error) {
	var claim claimedExport
	for {
		err := a.pool.QueryRow(ctx,
			`SELECT seq, object_id::text, salt, claimed_at FROM audit.claim_export($1, $2)`, a.batch, minSeq,
		).Scan(&claim.seq, &claim.objectID, &claim.salt, &claim.claimedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return claim, false, nil
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == sqlStateQueryCanceled && a.batch > minArchiveBatch && ctx.Err() == nil {
			a.batch = max(a.batch/2, minArchiveBatch)
			a.log.Warn("audit archive claim timed out, trying fewer rows", "rows", a.batch)
			continue
		}
		if err != nil {
			return claim, false, fmt.Errorf("claim: %w", err)
		}
		return claim, true, nil
	}
}

// exportNext stores one object. It holds no database connection while it uploads. A
// run that stops after the claim leaves the claim for the next one, which builds the
// same object: if that finds the object already stored, the upload had succeeded and
// only its record was missing.
func (a *Archive) exportNext(ctx context.Context, bucket *bucketState) (rows int, more bool, err error) {
	claim, found, err := a.claim(ctx, bucket.maxSeq+1)
	if err != nil || !found {
		return 0, false, wrapExport(err)
	}
	events, marked, err := a.claimedEvents(ctx, claim, a.tableDays)
	if err != nil {
		return 0, false, wrapExport(err)
	}
	prev, err := a.previousHash(ctx, bucket, claim.seq)
	if err != nil {
		return 0, false, wrapExport(err)
	}
	body, err := encodeArchiveObject(archiveHeader{
		Format: archiveFormat, Seq: claim.seq, PrevSHA256: prev, Count: len(events),
		ClaimedAt: claim.claimedAt.UTC(), RetentionDays: a.retentionDays, TableDays: a.tableDays,
	}, events)
	if err != nil {
		return 0, false, wrapExport(err)
	}

	stored, err := a.upload(ctx, claim, body, events)
	if err != nil {
		return 0, false, wrapExport(err)
	}
	header, _, err := decodeArchiveObject(stored.Body)
	if err != nil {
		return 0, false, wrapExport(fmt.Errorf("%s: %w", claim.key(), err))
	}
	if problem := lockProblem(stored, header); problem != "" {
		return 0, false, wrapExport(fmt.Errorf("%s: %s", claim.key(), problem))
	}
	hash := sha256Hex(stored.Body)
	var recorded bool
	if err := a.pool.QueryRow(ctx, `SELECT audit.finish_export($1, $2, $3)`,
		claim.seq, hash, stored.RetainUntil).Scan(&recorded); err != nil {
		return 0, false, wrapExport(fmt.Errorf("finish: %w", err))
	}
	if !recorded {
		return 0, false, wrapExport(fmt.Errorf("finish: export %d is not recorded", claim.seq))
	}
	bucket.maxSeq = max(bucket.maxSeq, claim.seq)
	bucket.hashes[claim.seq] = hash
	return len(events), marked >= a.batch, nil
}

func wrapExport(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("audit.Archive.exportNext: %w", err)
}

// upload stores the object, or finds the one an earlier attempt uploaded, and returns
// what the bucket holds. The lock is counted from whichever is later, now or the
// claim: the claim's time comes from the database, whose clock can be ahead, and a
// lock cannot be corrected once it is set too short.
func (a *Archive) upload(ctx context.Context, claim claimedExport, body []byte, events []archivedEvent) (*lockedObject, error) {
	from := time.Now()
	if claim.claimedAt.After(from) {
		from = claim.claimedAt
	}
	retainUntil := from.UTC().AddDate(0, 0, a.retentionDays).Truncate(time.Second).Add(time.Second)
	versionID, created, err := a.store.PutLocked(ctx, claim.key(), body, retainUntil)
	if err != nil {
		return nil, err
	}
	if !created {
		versions, err := a.store.Versions(ctx, claim.key())
		if err != nil {
			return nil, err
		}
		original, ok := originalVersions(versions)[claim.key()]
		if !ok {
			return nil, fmt.Errorf("%s exists but is not listed", claim.key())
		}
		versionID = original.VersionID
	}
	stored, err := a.store.Get(ctx, claim.key(), versionID)
	if err != nil {
		return nil, fmt.Errorf("read back: %w", err)
	}
	if created && !bytes.Equal(stored.Body, body) {
		return nil, fmt.Errorf("%s: the bucket returned other content than was stored", claim.key())
	}
	if !created {
		if err := a.checkResumed(ctx, claim, stored, events); err != nil {
			return nil, err
		}
	}
	return stored, nil
}

// checkResumed accepts an object stored by an earlier attempt that stopped before
// recording it. It must be that attempt's object: the same number, every row still
// marked with it, and nothing else but rows the retention sweep has taken since,
// which are gone from the table and older than it keeps.
func (a *Archive) checkResumed(ctx context.Context, claim claimedExport, stored *lockedObject, events []archivedEvent) error {
	header, storedEvents, err := decodeArchiveObject(stored.Body)
	if err != nil {
		return fmt.Errorf("%s: %w", claim.key(), err)
	}
	storedIDs := eventIDs(storedEvents)
	// The verification counts the lock and the oldest row an object may hold from its
	// header, so the header must be this claim's.
	if header.Seq != claim.seq || !header.ClaimedAt.Equal(claim.claimedAt.UTC().Truncate(time.Microsecond)) ||
		!validTableDays(header.TableDays) || header.RetentionDays < header.TableDays {
		return fmt.Errorf("%s has another export's header than export %d", claim.key(), claim.seq)
	}
	// A retention changed between the upload and now: the rows are those the upload
	// counted.
	if header.TableDays != a.tableDays {
		if events, _, err = a.claimedEvents(ctx, claim, header.TableDays); err != nil {
			return fmt.Errorf("%s: %w", claim.key(), err)
		}
	}
	if slices.ContainsFunc(eventIDs(events), func(id string) bool {
		return !slices.Contains(storedIDs, id)
	}) {
		return fmt.Errorf("%s holds other rows than export %d", claim.key(), claim.seq)
	}
	var gone []string
	for _, event := range storedEvents {
		if !slices.Contains(eventIDs(events), event.ID) {
			gone = append(gone, event.ID)
		}
	}
	if len(gone) == 0 {
		return nil
	}
	var swept bool
	err = a.pool.QueryRow(ctx, `
		SELECT NOT EXISTS (SELECT 1 FROM audit.events WHERE id = ANY($1::uuid[]))`, gone).Scan(&swept)
	if err != nil {
		return fmt.Errorf("%s: %w", claim.key(), err)
	}
	// Rows the sweep took went by the retention of the time, which is the shorter of the
	// upload's and today's.
	keptSince := a.now().UTC().AddDate(0, 0, -min(header.TableDays, a.tableDays))
	if !swept || slices.ContainsFunc(storedEvents, func(event archivedEvent) bool {
		return slices.Contains(gone, event.ID) && !event.CreatedAt.Before(keptSince)
	}) {
		return fmt.Errorf("%s holds rows export %d never had", claim.key(), claim.seq)
	}
	return nil
}

// previousHash is the hash of the first object under the number before seq: the one
// this run stored, or the one in the bucket. Nothing is there before the first object,
// or once that object's lock has ended and the bucket has removed it. An object too
// large to read is named by nothing either: the verification reports it, and the
// archive must not stop on what a stranger wrote. Any other failure stops the run,
// which tries again next hour rather than lock an object that names nothing.
func (a *Archive) previousHash(ctx context.Context, bucket *bucketState, seq int64) (string, error) {
	if hash, ok := bucket.hashes[seq-1]; ok {
		return hash, nil
	}
	original, ok := bucket.originals[seq-1]
	if !ok {
		return "", nil
	}
	object, err := a.store.Get(ctx, original.Key, original.VersionID)
	if errors.Is(err, errTooLarge) {
		a.log.Warn("audit archive: the previous object cannot be read", "key", original.Key, "error", err)
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("previous object: %w", err)
	}
	hash := sha256Hex(object.Body)
	bucket.hashes[seq-1] = hash
	return hash, nil
}

// validTableDays reports a retention the table can have had.
func validTableDays(days int) bool { return days >= minTableDays && days <= maxRetentionDays }

// lockProblem says what is wrong with an object's lock, or nothing. The object must
// be held for the retention its header names, counted from its claim, and that must
// cover what the table kept when it was claimed. A retention raised since leaves
// earlier objects as they were written.
func lockProblem(object *lockedObject, header archiveHeader) string {
	if object.Mode != lockModeCompliance {
		return fmt.Sprintf("lock mode is %q, want %s", object.Mode, lockModeCompliance)
	}
	if !validTableDays(header.TableDays) || header.RetentionDays < header.TableDays {
		return fmt.Sprintf("written for %d days, the table kept rows %d", header.RetentionDays, header.TableDays)
	}
	if until := header.ClaimedAt.UTC().AddDate(0, 0, header.RetentionDays).Truncate(time.Second); object.RetainUntil.Before(until) {
		return fmt.Sprintf("locked until %s, want at least %s",
			object.RetainUntil.UTC().Format(time.RFC3339), until.Format(time.RFC3339))
	}
	return ""
}

// Pending counts the rows that should have reached the archive by now: not claimed,
// or claimed by an export that has not finished for that long. A row dated in the
// future is late too: nothing the application writes is.
func (a *Archive) Pending(ctx context.Context) (int64, error) {
	var pending int64
	err := a.pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT 1 FROM audit.events
			WHERE export_seq IS NULL AND (created_at < now() - $1::interval OR created_at > now() + $1::interval)
			UNION ALL
			SELECT 1 FROM audit.events e JOIN audit.exports x ON x.seq = e.export_seq
			WHERE x.uploaded_at IS NULL AND x.claimed_at < now() - $1::interval
			LIMIT $2
		) late`, a.overdue, pendingCap).Scan(&pending)
	if err != nil {
		return 0, fmt.Errorf("audit.Archive.Pending: %w", err)
	}
	return pending, nil
}

// archiveHeader is the first line of an object. prev_sha256 is the hash of the object
// before it, so a missing or replaced object shows from the bucket alone.
type archiveHeader struct {
	Format        int       `json:"format"`
	Seq           int64     `json:"seq"`
	PrevSHA256    string    `json:"prev_sha256"`
	Count         int       `json:"count"`
	ClaimedAt     time.Time `json:"claimed_at"`
	RetentionDays int       `json:"retention_days"`
	// TableDays is how long the table kept rows when the export was claimed. No row older
	// than that, counted from the claim, is in the object.
	TableDays int `json:"table_days"`
}

// archivedEvent is one row of audit.events as the archive keeps it. An operator's row
// is kept whole. Of an unauthenticated row the request body is kept only as a digest:
// it can hold a person's data, sent with a token that had expired. Of a person's row
// everything that could name them is a digest: the archive cannot be edited when that
// person's account is deleted. The actor is the hash of a random identifier, which
// cannot be guessed back; the other digests are keyed with the export's salt, which
// only the database holds.
type archivedEvent struct {
	ID             string          `json:"id"`
	OrganizationID *string         `json:"organization_id,omitempty"`
	Actor          string          `json:"actor,omitempty"`
	ActorSHA256    string          `json:"actor_sha256,omitempty"`
	Action         string          `json:"action"`
	Resource       string          `json:"resource"`
	StatusCode     int             `json:"status_code"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	MetadataDigest string          `json:"metadata_digest"`
	IP             string          `json:"ip,omitempty"`
	UserAgent      string          `json:"user_agent,omitempty"`
	ClientDigest   string          `json:"client_digest,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

// auditRow is a row of audit.events as the table holds it now.
type auditRow struct {
	id, actor, action, resource, ip, userAgent, metadata string
	organizationID                                       *string
	statusCode                                           int
	createdAt                                            time.Time
	exportSeq                                            *int64
}

// auditRowCols reads metadata as Postgres writes it, so its digest is over the same
// text each time.
const auditRowCols = `id::text, organization_id, actor, action, resource, status_code, metadata::text,
	ip, user_agent, created_at, export_seq`

func scanAuditRow(row pgx.CollectableRow) (auditRow, error) {
	var r auditRow
	err := row.Scan(&r.id, &r.organizationID, &r.actor, &r.action, &r.resource, &r.statusCode,
		&r.metadata, &r.ip, &r.userAgent, &r.createdAt, &r.exportSeq)
	return r, err
}

func digest(salt string, parts ...string) string {
	mac := hmac.New(sha256.New, []byte(salt))
	for _, part := range parts {
		mac.Write([]byte(strconv.Itoa(len(part)) + ":" + part))
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func isOperatorActor(actor string) bool { return strings.HasPrefix(actor, operatorActorPrefix) }

// isPersonActor reports whether a row's actor is a person whose data may have to be
// erased. audit.anonymize_actor refuses every other actor.
func isPersonActor(actor string) bool {
	return !isOperatorActor(actor) && actor != anonymousActor && actor != deletedActor
}

func (r auditRow) archived(salt string) archivedEvent {
	event := archivedEvent{
		ID: r.id, OrganizationID: r.organizationID, Action: r.action, Resource: r.resource,
		StatusCode: r.statusCode, MetadataDigest: digest(salt, r.metadata), CreatedAt: r.createdAt.UTC(),
	}
	switch {
	case isOperatorActor(r.actor):
		event.Actor, event.Metadata, event.IP, event.UserAgent = r.actor, json.RawMessage(r.metadata), r.ip, r.userAgent
	case r.actor == anonymousActor:
		event.Actor, event.IP, event.UserAgent = r.actor, r.ip, r.userAgent
	case r.actor == deletedActor:
		event.Actor, event.ClientDigest = r.actor, digest(salt, r.ip, r.userAgent)
	default:
		event.ActorSHA256, event.ClientDigest = sha256Hex([]byte(r.actor)), digest(salt, r.ip, r.userAgent)
	}
	return event
}

// claimedEvents reads the rows of an export, and how many rows the export marked. More
// rows than one object holds means rows were marked by hand; nothing is stored for it.
// A row older than the table kept, counted from the claim, is the retention sweep's:
// it stays marked and out of the object, which therefore never holds a row its
// header's dates rule out.
func (a *Archive) claimedEvents(ctx context.Context, claim claimedExport, tableDays int) ([]archivedEvent, int, error) {
	rows, err := a.pool.Query(ctx,
		`SELECT `+auditRowCols+` FROM audit.events WHERE export_seq = $1 ORDER BY created_at, id LIMIT $2`,
		claim.seq, archiveBatch+1)
	if err != nil {
		return nil, 0, fmt.Errorf("read: %w", err)
	}
	marked, err := pgx.CollectRows(rows, scanAuditRow)
	if err != nil {
		return nil, 0, fmt.Errorf("read: %w", err)
	}
	if len(marked) > archiveBatch {
		return nil, 0, fmt.Errorf("export %d is marked on more than %d rows", claim.seq, archiveBatch)
	}
	kept := claim.claimedAt.UTC().AddDate(0, 0, -tableDays)
	events := make([]archivedEvent, 0, len(marked))
	for _, row := range marked {
		if !row.createdAt.Before(kept) {
			events = append(events, row.archived(claim.salt))
		}
	}
	return events, len(marked), nil
}

func eventIDs(events []archivedEvent) []string {
	ids := make([]string, len(events))
	for i, event := range events {
		ids[i] = event.ID
	}
	return ids
}

// encodeArchiveObject writes the header and one line per event, compressed. The same
// header and rows give the same bytes.
func encodeArchiveObject(header archiveHeader, events []archivedEvent) ([]byte, error) {
	var out bytes.Buffer
	zip := gzip.NewWriter(&out)
	lines := json.NewEncoder(zip)
	if err := lines.Encode(header); err != nil {
		return nil, err
	}
	for _, event := range events {
		if err := lines.Encode(event); err != nil {
			return nil, err
		}
	}
	if err := zip.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// decodeArchiveObject reads an object, which anyone with write access to the bucket
// can have written: it is bounded, and refuses anything the archive would not write.
func decodeArchiveObject(body []byte) (archiveHeader, []archivedEvent, error) {
	var header archiveHeader
	zip, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return header, nil, err
	}
	lines := json.NewDecoder(io.LimitReader(zip, maxDecodedBytes))
	if err := lines.Decode(&header); err != nil {
		return header, nil, err
	}
	if header.Format != archiveFormat {
		return header, nil, fmt.Errorf("unknown format %d", header.Format)
	}
	if header.Count > archiveBatch {
		return header, nil, fmt.Errorf("the header says %d events, more than an object holds", header.Count)
	}
	events := make([]archivedEvent, 0, header.Count)
	for lines.More() {
		var event archivedEvent
		if err := lines.Decode(&event); err != nil {
			return header, nil, err
		}
		if !uuidPattern.MatchString(event.ID) {
			return header, nil, fmt.Errorf("an event id %q is not one the table writes", event.ID)
		}
		if events = append(events, event); len(events) > header.Count {
			return header, nil, fmt.Errorf("more events than the header's %d", header.Count)
		}
	}
	if len(events) != header.Count {
		return header, nil, fmt.Errorf("%d events, the header says %d", len(events), header.Count)
	}
	return header, events, nil
}
