package audit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// maxListedProblems bounds what a report prints per kind; the counts are complete.
const maxListedProblems = 50

// maxRetentionDays bounds a retention an object's header may claim.
const maxRetentionDays = 3650

// numberRangePattern is how a known gap in the numbering is written.
var numberRangePattern = regexp.MustCompile(`^numbers [0-9]+-[0-9]+$`)

// The kinds of problem a verification reports, each printed with what it concerns.
const (
	problemAltered   = "altered"     // a row differs from its archived copy
	problemMissing   = "missing"     // an archived row is gone before its time
	problemUnstored  = "unstored"    // a row is marked exported and is in no object of that number
	problemLock      = "weak lock"   // an object is not held as it should be
	problemObject    = "object"      // an object is unreadable, misnamed or stored twice under a number
	problemChain     = "chain"       // an object is missing or does not follow the one before it
	problemOverwrite = "overwritten" // something was written over a locked object
	problemClock     = "clock"       // the database's clock is far from this host's

	// clockTolerance is how far the database's clock may be from this host's.
	clockTolerance = 15 * time.Minute
)

// VerifyReport is what a verification found. Rows are counted once per archived copy.
type VerifyReport struct {
	Objects    int
	Matched    int
	Anonymized int // the person's account was deleted since; nothing else differs
	// Deleting lists rows anonymized while their account's deletion is under way: not a
	// finding, but shown, since it rests on a task the database records.
	Deleting []string
	// CopyExpired counts rows still in the table whose copy a lifecycle rule has removed:
	// the table's retention was raised after they were copied. CopyExpiredIDs lists some.
	CopyExpired    int
	CopyExpiredIDs []string
	Expired        int // past the table's own retention
	Known          int // missing, and listed as known
	Waiting        int64
	problems       map[string][]string
}

// OK reports whether the table still says what the archive says, and the archive
// holds everything it should by now: one that never ran would agree with any table.
func (r *VerifyReport) OK() bool { return len(r.problems) == 0 && r.Waiting == 0 }

// Problems lists what was found of one kind.
func (r *VerifyReport) Problems(kind string) []string { return r.problems[kind] }

func (r *VerifyReport) addf(kind, format string, args ...any) {
	r.problems[kind] = append(r.problems[kind], fmt.Sprintf(format, args...))
}

// Write prints the report.
func (r *VerifyReport) Write(w io.Writer) {
	fmt.Fprintf(w, "objects=%d matched=%d anonymized=%d expired=%d known=%d\n",
		r.Objects, r.Matched, r.Anonymized, r.Expired, r.Known)
	fmt.Fprintf(w, "late for export: %d\n", r.Waiting)
	if r.CopyExpired > 0 {
		fmt.Fprintf(w, "rows whose copy expired before the table lets them go: %d\n", r.CopyExpired)
		for _, id := range r.CopyExpiredIDs[:min(len(r.CopyExpiredIDs), maxListedProblems)] {
			fmt.Fprintf(w, "  %s\n", id)
		}
	}
	if len(r.Deleting) > 0 {
		fmt.Fprintf(w, "anonymized while the account's deletion is under way: %d\n", len(r.Deleting))
		for _, id := range r.Deleting[:min(len(r.Deleting), maxListedProblems)] {
			fmt.Fprintf(w, "  %s\n", id)
		}
	}
	for _, kind := range slices.Sorted(maps.Keys(r.problems)) {
		found := r.problems[kind]
		fmt.Fprintf(w, "%s: %d\n", kind, len(found))
		for _, problem := range found[:min(len(found), maxListedProblems)] {
			fmt.Fprintf(w, "  %s\n", problem)
		}
		if more := len(found) - maxListedProblems; more > 0 {
			fmt.Fprintf(w, "  and %d more\n", more)
		}
	}
	if r.OK() {
		fmt.Fprintln(w, "RESULT: PASS")
	} else {
		fmt.Fprintln(w, "RESULT: FAIL")
	}
}

// ReadKnownIDs reads what is known to be wrong and has been looked into, one per line:
// the id of a row a restore lost, the name of an object the database has no record of
// (a restore's, or one a stranger put there), or `numbers X-Y`, a gap in the numbering.
// Blank lines and lines starting with # are skipped.
func ReadKnownIDs(r io.Reader) (map[string]bool, error) {
	known := map[string]bool{}
	lines := bufio.NewScanner(r)
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, isObject := archiveSeq(line); !uuidPattern.MatchString(line) && !isObject && !numberRangePattern.MatchString(line) {
			return nil, fmt.Errorf("audit.ReadKnownIDs: %q is not an event id, an object name or a number range", line)
		}
		known[line] = true
	}
	return known, lines.Err()
}

// exportRecord is a row of audit.exports.
type exportRecord struct {
	objectID, salt string
	claimedAt      time.Time
	uploadedAt     *time.Time
}

// verification is the state of one Verify run.
type verification struct {
	*Archive
	report *VerifyReport
	known  map[string]bool
	// start is this host's clock, for the bucket's locks. dbNow is the database's,
	// which wrote every time the table and the ledger hold, read before the bucket is
	// listed; ledgerNow is the database's when the ledger was read, after the listing.
	start     time.Time
	dbNow     time.Time
	ledgerNow time.Time
	// maxListed is the highest number among the archive's objects, and newest the latest
	// of them to be written, by the bucket's own clock. Markers, stray names, later
	// versions and objects listed as known do not count: whoever can write to the bucket
	// could set both with them.
	maxListed int64
	newest    time.Time
	// deleting holds the accounts whose deletion is under way.
	deleting map[string]bool
	ledger   map[int64]exportRecord
	living   map[string]bool
	objects  map[int64][]string // the hash of each readable object, by number
	// counted holds how many objects that are not listed as known each number has: a
	// known one takes its place in the numbering and is not stored twice by being there.
	counted map[int64]int
}

// Verify compares the whole archive with audit.events. It trusts the bucket, where a
// stored version cannot change, and reads the first version of each object: whoever
// can write to the bucket can still add a newer one, or a marker that hides it. The
// database's own records of what it exported give the salts, and excuse the exports
// that may not have reached the bucket yet; whoever is checked can edit them, so they
// excuse nothing more.
func (a *Archive) Verify(ctx context.Context, known map[string]bool) (*VerifyReport, error) {
	if err := a.CheckLock(ctx); err != nil {
		return nil, err
	}
	v := &verification{
		Archive: a, report: &VerifyReport{problems: map[string][]string{}}, known: known,
		start: time.Now().UTC(), objects: map[int64][]string{}, counted: map[int64]int{}, deleting: map[string]bool{},
	}
	if err := a.pool.QueryRow(ctx, `SELECT pg_catalog.now()`).Scan(&v.dbNow); err != nil {
		return nil, fmt.Errorf("audit.Archive.Verify: %w", err)
	}
	// The database's clock decides what has expired, so one set far from this host's
	// could excuse deletions.
	if skew := v.dbNow.Sub(v.start); skew > clockTolerance || skew < -clockTolerance {
		v.report.addf(problemClock, "the database's clock is %s from this host's", skew.Round(time.Minute))
	}
	// Listed before the ledger is read: an export that finishes in between is then in
	// the ledger and not in the listing, which the check of marks allows for.
	versions, err := a.store.Versions(ctx, archivePrefix)
	if err != nil {
		return nil, fmt.Errorf("audit.Archive.Verify: %w", err)
	}
	if v.ledger, v.ledgerNow, err = a.readLedger(ctx); err != nil {
		return nil, fmt.Errorf("audit.Archive.Verify: %w", err)
	}
	for key, original := range originalVersions(versions) {
		if seq, ok := archiveSeq(key); ok && !v.known[key] {
			v.maxListed = max(v.maxListed, seq)
			if original.LastModified.After(v.newest) {
				v.newest = original.LastModified
			}
		}
	}

	byKey := map[string][]objectVersion{}
	for _, version := range versions {
		byKey[version.Key] = append(byKey[version.Key], version)
	}
	originals := originalVersions(versions)
	for _, key := range slices.Sorted(maps.Keys(byKey)) {
		original, ok := originals[key]
		if !ok {
			// Markers only: a lifecycle rule removed the object once its lock ended.
			continue
		}
		seq, ok := archiveSeq(key)
		if !ok {
			v.report.addf(problemObject, "%s: not a name the archive writes", key)
			continue
		}
		if err := v.checkObject(ctx, seq, original, len(byKey[key]) > 1); err != nil {
			return nil, fmt.Errorf("audit.Archive.Verify: %s: %w", key, err)
		}
	}
	v.checkNumbers()
	if err := v.checkMarks(ctx); err != nil {
		return nil, fmt.Errorf("audit.Archive.Verify: %w", err)
	}
	if v.report.Waiting, err = a.Pending(ctx); err != nil {
		return nil, fmt.Errorf("audit.Archive.Verify: %w", err)
	}
	return v.report, nil
}

// archiveSeq reads the number out of a name the archive writes.
func archiveSeq(key string) (int64, bool) {
	match := archiveKeyPattern.FindStringSubmatch(key)
	if match == nil {
		return 0, false
	}
	seq, err := strconv.ParseInt(match[1], 10, 64)
	return seq, err == nil && seq > 0
}

func (a *Archive) readLedger(ctx context.Context) (map[int64]exportRecord, time.Time, error) {
	var now time.Time
	if err := a.pool.QueryRow(ctx, `SELECT pg_catalog.now()`).Scan(&now); err != nil {
		return nil, now, fmt.Errorf("ledger: %w", err)
	}
	rows, err := a.pool.Query(ctx, `SELECT seq, object_id::text, salt, claimed_at, uploaded_at FROM audit.exports`)
	if err != nil {
		return nil, now, fmt.Errorf("ledger: %w", err)
	}
	out := map[int64]exportRecord{}
	var seq int64
	var record exportRecord
	if _, err := pgx.ForEachRow(rows, []any{&seq, &record.objectID, &record.salt, &record.claimedAt, &record.uploadedAt}, func() error {
		out[seq] = record
		return nil
	}); err != nil {
		return nil, now, fmt.Errorf("ledger: %w", err)
	}
	return out, now, nil
}

// checkObject checks one object and every row in it. What the object holds is
// reported, never allowed to stop the run: anyone with write access to the bucket
// decides what it holds.
func (v *verification) checkObject(ctx context.Context, seq int64, original objectVersion, writtenOver bool) error {
	key := original.Key
	object, err := v.store.Get(ctx, key, original.VersionID)
	var answered *s3Error
	if errors.As(err, &answered) || errors.Is(err, errTooLarge) {
		v.report.addf(problemObject, "%s: %v", key, err)
		return nil
	}
	if err != nil {
		return err
	}
	header, events, err := decodeArchiveObject(object.Body)
	if err != nil {
		v.report.addf(problemObject, "%s: %v", key, err)
		return nil
	}
	v.report.Objects++
	hash := sha256Hex(object.Body)
	v.objects[seq] = append(v.objects[seq], hash)
	if v.known[key] {
		// Looked into already: it takes its place in the numbering, and nothing else.
		return nil
	}
	v.counted[seq]++
	// No real export holds a row older than the table kept when it was claimed. The
	// header says how long that was, within what a retention can be.
	tableDays := v.tableDays
	if header.TableDays > tableDays && header.TableDays <= maxRetentionDays {
		tableDays = header.TableDays
	}
	oldest := header.ClaimedAt.AddDate(0, 0, -tableDays).Add(-time.Hour)
	for _, event := range events {
		if event.CreatedAt.Before(oldest) {
			v.report.addf(problemObject, "%s: holds %s, dated before the table kept rows when it was claimed", key, event.ID)
		}
	}
	if header.Seq != seq {
		v.report.addf(problemObject, "%s: its header says number %d", key, header.Seq)
	}
	if problem := lockProblem(object, header); problem != "" {
		v.report.addf(problemLock, "%s: %s", key, problem)
	}
	// A lifecycle rule marks an object deleted once its lock has ended; before that,
	// anything written over it was written by someone who should not have.
	if writtenOver && v.start.Before(object.RetainUntil) {
		v.report.addf(problemOverwrite, "%s", key)
	}
	v.checkChain(key, seq, header)

	salt := ""
	if record, ok := v.ledger[seq]; ok && strings.HasSuffix(key, "-"+record.objectID+".ndjson.gz") {
		salt = record.salt
		if !header.ClaimedAt.Equal(record.claimedAt.UTC().Truncate(time.Microsecond)) {
			v.report.addf(problemObject, "%s: its header says it was claimed at another time than the database recorded", key)
		}
	} else {
		v.report.addf(problemObject, "%s: the database has no record of it, so its digests cannot be checked", key)
	}
	return v.checkEvents(ctx, key, seq, salt, events)
}

// checkChain needs the object before this one to have been read already; numbers are
// read in order.
func (v *verification) checkChain(key string, seq int64, header archiveHeader) {
	previous, ok := v.objects[seq-1]
	if !ok {
		return
	}
	if !slices.Contains(previous, header.PrevSHA256) {
		v.report.addf(problemChain, "%s: names an object before it that is not there", key)
	}
}

// checkNumbers reports a number stored twice, and a gap. Numbers below the lowest one
// stored are objects whose locks ended, and gone with them are rows past the table's
// own retention.
func (v *verification) checkNumbers() {
	numbers := slices.Sorted(maps.Keys(v.objects))
	for i, seq := range numbers {
		if n := v.counted[seq]; n > 1 {
			v.report.addf(problemObject, "number %d is stored %d times", seq, n)
		}
		if i > 0 && seq != numbers[i-1]+1 && !v.knownGap(numbers[i-1]+1, seq-1) {
			v.report.addf(problemChain, "numbers %d-%d are missing", numbers[i-1]+1, seq-1)
		}
	}
}

// knownGap reports whether every number from first to last is in a gap listed as known.
func (v *verification) knownGap(first, last int64) bool {
	for entry := range v.known {
		var from, to int64
		if _, err := fmt.Sscanf(entry, "numbers %d-%d", &from, &to); err == nil && from <= first && last <= to {
			return true
		}
	}
	return false
}

func (v *verification) keptSince() time.Time {
	// The retention sweep runs hourly and a verification takes a while; the margin
	// keeps a row the sweep is about to take from reading as an alteration.
	return v.dbNow.UTC().AddDate(0, 0, -v.tableDays).Add(archiveOverdue)
}

func (v *verification) checkEvents(ctx context.Context, key string, seq int64, salt string, events []archivedEvent) error {
	ids := eventIDs(events)
	rows, err := v.pool.Query(ctx, `SELECT `+auditRowCols+` FROM audit.events WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return err
	}
	found, err := pgx.CollectRows(rows, scanAuditRow)
	if err != nil {
		return err
	}
	current := map[string]auditRow{}
	for _, row := range found {
		current[row.id] = row
	}

	for _, event := range events {
		row, exists := current[event.ID]
		switch {
		case !exists && event.CreatedAt.Before(v.keptSince()):
			v.report.Expired++
		case !exists && v.known[event.ID]:
			v.report.Known++
		case !exists:
			v.report.addf(problemMissing, "%s (%s)", event.ID, key)
		default:
			changed, anonymized, err := v.compare(ctx, event, row, salt)
			switch {
			case err != nil:
				return err
			case len(changed) > 0:
				v.report.addf(problemAltered, "%s: %s (%s)", event.ID, strings.Join(changed, ", "), key)
			case anonymized:
				v.report.Anonymized++
			default:
				v.report.Matched++
			}
		}
	}

	// The other direction: rows marked with this number must be in an object of it.
	rows, err = v.pool.Query(ctx, `
		SELECT id::text FROM audit.events
		WHERE export_seq = $1 AND created_at >= $2 AND id <> ALL($3::uuid[])`, seq, v.keptSince(), ids)
	if err != nil {
		return err
	}
	extra, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, id := range extra {
		v.report.addf(problemUnstored, "%s: marked with %d and not in %s", id, seq, key)
	}
	return nil
}

// compare names the columns in which a row no longer matches its archived copy. A
// person's row may have been anonymized since: that replaces the actor with
// deleted_user and clears the address and client, and it counts only when the
// person's account is gone. Without the salt only what the copy holds in the clear is
// compared.
func (v *verification) compare(ctx context.Context, event archivedEvent, row auditRow, salt string) (changed []string, anonymized bool, err error) {
	differs := func(column string, different bool) {
		if different {
			changed = append(changed, column)
		}
	}
	differs("organization_id", (event.OrganizationID == nil) != (row.organizationID == nil) ||
		(event.OrganizationID != nil && *event.OrganizationID != *row.organizationID))
	differs("action", event.Action != row.action)
	differs("resource", event.Resource != row.resource)
	differs("status_code", event.StatusCode != row.statusCode)
	differs("created_at", !event.CreatedAt.Equal(row.createdAt))
	if salt != "" {
		differs("metadata", event.MetadataDigest != digest(salt, row.metadata))
	}

	switch {
	case event.ActorSHA256 != "" && row.actor == deletedActor:
		differs("ip, user_agent", row.ip != "" || row.userAgent != "")
		living, err := v.livingAccount(ctx, event.ActorSHA256)
		if err != nil {
			return nil, false, err
		}
		if living && v.deleting[event.ActorSHA256] && len(changed) == 0 {
			v.report.Deleting = append(v.report.Deleting, event.ID)
			return nil, false, nil
		}
		differs("actor (the account still exists)", living)
		return changed, len(changed) == 0, nil
	case event.ActorSHA256 != "":
		differs("actor", !isPersonActor(row.actor) || sha256Hex([]byte(row.actor)) != event.ActorSHA256)
	default:
		differs("actor", event.Actor != row.actor)
	}
	switch {
	case event.Actor == anonymousActor || isOperatorActor(event.Actor):
		differs("ip", event.IP != row.ip)
		differs("user_agent", event.UserAgent != row.userAgent)
	case salt != "":
		differs("ip, user_agent", event.ClientDigest != digest(salt, row.ip, row.userAgent))
	}
	if salt == "" && isOperatorActor(event.Actor) {
		differs("metadata", canonicalJSON(string(event.Metadata)) != canonicalJSON(row.metadata))
	}
	return changed, false, nil
}

// canonicalJSON writes JSON the way the archive's encoder does: compact, with <, >
// and & escaped.
func canonicalJSON(text string) string {
	var compact, escaped bytes.Buffer
	if err := json.Compact(&compact, []byte(text)); err != nil {
		return text
	}
	json.HTMLEscape(&escaped, compact.Bytes())
	return escaped.String()
}

// livingAccount reports whether an account whose identifier hashes to actorSHA256
// still exists. The set is read once, and only when an anonymized row is met.
func (v *verification) livingAccount(ctx context.Context, actorSHA256 string) (bool, error) {
	if v.living == nil {
		// An account whose deletion is under way has had its rows anonymized first and is
		// still there. A task days old is not under way any more.
		rows, err := v.pool.Query(ctx, `
			SELECT u.auth_sub, EXISTS (
				SELECT 1 FROM account.tasks t
				WHERE t.auth_sub = u.auth_sub AND t.kind = 'delete_account'
				  AND t.status IN ('pending', 'processing') AND t.created_at > pg_catalog.now() - interval '3 days'
			)
			FROM account.users u`)
		if err != nil {
			return false, fmt.Errorf("accounts: %w", err)
		}
		v.living, v.deleting = map[string]bool{}, map[string]bool{}
		var sub string
		var deleting bool
		if _, err := pgx.ForEachRow(rows, []any{&sub, &deleting}, func() error {
			v.living[sha256Hex([]byte(sub))] = true
			if deleting {
				v.deleting[sha256Hex([]byte(sub))] = true
			}
			return nil
		}); err != nil {
			return false, fmt.Errorf("accounts: %w", err)
		}
	}
	return v.living[actorSHA256], nil
}

// checkMarks finds rows marked with a number no object has. An export still in flight
// has its rows marked and no object yet: it is late, which Pending counts, only once
// it is older than the overdue time.
func (v *verification) checkMarks(ctx context.Context) error {
	rows, err := v.pool.Query(ctx, `
		SELECT DISTINCT export_seq FROM audit.events WHERE export_seq IS NOT NULL AND created_at >= $1`, v.keptSince())
	if err != nil {
		return fmt.Errorf("marks: %w", err)
	}
	numbers, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return fmt.Errorf("marks: %w", err)
	}
	// Objects listed as known are no evidence of where the stored numbers begin.
	lowest := int64(0)
	if stored := slices.Sorted(maps.Keys(v.counted)); len(stored) > 0 {
		lowest = stored[0]
	}
	for _, seq := range numbers {
		if _, stored := v.objects[seq]; stored {
			continue
		}
		// Below the lowest object still stored, objects have reached the end of their
		// lock, and a rule may have removed them. Their rows are still in the table when
		// its retention was raised since they were copied: no longer copied, which is
		// shown, not failed.
		copyExpired := seq < lowest
		if v.inFlight(seq) {
			// The exporter writes an object within minutes of its claim. An export in
			// flight while nothing reached the bucket for longer than a run's interval is
			// a ledger kept fresh by hand.
			if v.newest.Before(v.start.Add(-v.overdue - time.Hour)) {
				v.report.addf(problemChain, "export %d is in flight, and the newest object is from %s",
					seq, v.newest.UTC().Format(time.RFC3339))
			}
			continue
		}
		rows, err := v.pool.Query(ctx, `
			SELECT id::text FROM audit.events WHERE export_seq = $1 AND created_at >= $2 ORDER BY id LIMIT $3`,
			seq, v.keptSince(), maxListedProblems+1)
		if err != nil {
			return fmt.Errorf("marks: %w", err)
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return fmt.Errorf("marks: %w", err)
		}
		if copyExpired {
			var count int
			if err := v.pool.QueryRow(ctx, `
				SELECT count(*) FROM audit.events WHERE export_seq = $1 AND created_at >= $2`, seq, v.keptSince()).Scan(&count); err != nil {
				return fmt.Errorf("marks: %w", err)
			}
			v.report.CopyExpired += count
			v.report.CopyExpiredIDs = append(v.report.CopyExpiredIDs, ids...)
			continue
		}
		for _, id := range ids {
			v.report.addf(problemUnstored, "%s: marked with %d, which no object in the bucket has", id, seq)
		}
	}
	return nil
}

// inFlight reports an export that may have no object in the listing yet: numbered
// above the bucket's highest, and claimed within the overdue time and not finished, or
// finished after the bucket was listed (a run catching up stores several meanwhile). A
// time the database's clock had not reached was not written by the exporter, so it
// excuses nothing.
func (v *verification) inFlight(seq int64) bool {
	record, ok := v.ledger[seq]
	if !ok || seq <= v.maxListed {
		return false
	}
	if record.uploadedAt == nil {
		return record.claimedAt.After(v.dbNow.Add(-v.overdue)) && !record.claimedAt.After(v.ledgerNow)
	}
	return !record.uploadedAt.Before(v.dbNow.Add(-time.Minute)) && !record.uploadedAt.After(v.ledgerNow)
}
