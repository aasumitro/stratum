package audit

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/aasumitro/stratum/internal/platform/db"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/otel"
)

// responseBodyWriter buffers the response body so the audit middleware
// can capture error responses without affecting the actual response.
type responseBodyWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w *responseBodyWriter) Write(b []byte) (int, error) {
	if w.body.Len() < maxBodyBytes {
		remaining := maxBodyBytes - w.body.Len()
		if len(b) > remaining {
			w.body.Write(b[:remaining])
		} else {
			w.body.Write(b)
		}
	}
	return w.ResponseWriter.Write(b)
}

const (
	bufferSize    = 4096
	batchSize     = 100
	flushInterval = 500 * time.Millisecond
	maxBodyBytes  = 4096
	cleanupTick   = 1 * time.Hour

	// dropLogInterval keeps an overflowing buffer from flooding the log at the
	// exact moment the service is already overloaded.
	dropLogInterval = 1 * time.Minute

	anonymousActor = "anonymous"

	auditBeforeKey = "audit.before"
	auditAfterKey  = "audit.after"
)

var (
	emptyMetadata = []byte("{}")
	nulEscape     = []byte(`\u0000`)
)

// SetBefore stores the state before a mutation so the audit middleware
// can include it in the event metadata.
func SetBefore(c *gin.Context, v any) { c.Set(auditBeforeKey, v) }

// SetAfter stores the state after a successful mutation for audit metadata.
func SetAfter(c *gin.Context, v any) { c.Set(auditAfterKey, v) }

type EventRecord struct {
	ID             string    `json:"id"`
	OrganizationID *string   `json:"organization_id,omitempty"`
	Actor          string    `json:"actor"`
	Action         string    `json:"action"`
	Resource       string    `json:"resource"`
	StatusCode     int       `json:"status_code"`
	Metadata       []byte    `json:"metadata,omitempty"`
	IP             string    `json:"ip"`
	UserAgent      string    `json:"user_agent"`
	CreatedAt      time.Time `json:"created_at"`
}

type event struct {
	organizationID *string
	actor          string
	action         string
	resource       string
	statusCode     int
	metadata       []byte
	ip             string
	userAgent      string
}

// Writer buffers audit events in a channel and batch-inserts them
// into the database on a timer or when the batch is full.
type Writer struct {
	pool          *pgxpool.Pool
	ch            chan event
	logger        *slog.Logger
	retentionDays int
	done          chan struct{}

	// stopCleanup and cleanupDone are set by NewWriter, which starts runCleanup.
	stopCleanup context.CancelFunc
	cleanupDone chan struct{}

	// droppedSinceLog counts events lost to a full buffer since run last
	// reported them. The request path only increments it; run owns the log.
	droppedSinceLog atomic.Int64
	dropped         metric.Int64Counter
	writeFailures   metric.Int64Counter
	authFailures    metric.Int64Counter
	sweepFailures   metric.Int64Counter
}

// NewWriter starts a background goroutine that drains the channel
// and writes batches to the database. Call Stop to flush remaining events.
func NewWriter(pool *pgxpool.Pool, logger *slog.Logger, retentionDays int) *Writer {
	w := newWriter(pool, logger, retentionDays, bufferSize)
	cleanupCtx, stopCleanup := context.WithCancel(context.Background())
	w.stopCleanup = stopCleanup
	w.cleanupDone = make(chan struct{})
	go w.run()
	go w.runCleanup(cleanupCtx)
	return w
}

func newWriter(pool *pgxpool.Pool, logger *slog.Logger, retentionDays, buffer int) *Writer {
	return &Writer{
		pool:          pool,
		ch:            make(chan event, buffer),
		logger:        logger,
		retentionDays: retentionDays,
		done:          make(chan struct{}),
		dropped:       otel.Counter("stratum.audit.events.dropped", "Audit events lost because the write buffer was full."),
		writeFailures: otel.Counter("stratum.audit.write.failures", "Audit batches the database refused."),
		authFailures:  otel.Counter("stratum.http.auth.failures", "Requests answered 401 or 403."),
		sweepFailures: otel.Counter("stratum.retention.sweep.failures", "Retention sweeps that failed and will retry on the next tick."),
	}
}

func (w *Writer) run() {
	defer close(w.done)
	batch := make([]event, 0, batchSize)
	flushTicker := time.NewTicker(flushInterval)
	dropLogTicker := time.NewTicker(dropLogInterval)
	defer flushTicker.Stop()
	defer dropLogTicker.Stop()

	for {
		select {
		case e, ok := <-w.ch:
			if !ok {
				if len(batch) > 0 {
					w.flush(batch)
				}
				w.logDropped()
				return
			}
			batch = append(batch, e)
			if len(batch) >= batchSize {
				w.flush(batch)
				batch = batch[:0]
			}
		case <-flushTicker.C:
			if len(batch) > 0 {
				w.flush(batch)
				batch = batch[:0]
			}
		case <-dropLogTicker.C:
			w.logDropped()
		}
	}
}

// runCleanup is separate from run because a retention sweep can take seconds, and
// nothing drains the event buffer while run is busy: under load that would drop the
// events the sweep is meant to protect.
func (w *Writer) runCleanup(ctx context.Context) {
	defer close(w.cleanupDone)
	ticker := time.NewTicker(cleanupTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.cleanup(ctx)
		}
	}
}

// logDropped reports events lost since the previous report, if any.
func (w *Writer) logDropped() {
	if n := w.droppedSinceLog.Swap(0); n > 0 {
		w.logger.Warn("audit events dropped", "count", n)
	}
}

// InsertDirect records a single audit event immediately, bypassing the
// batching Writer. For call sites outside the /api/v1 middleware chain
// (e.g. webhook routes) that have no request to hang Middleware off of but
// still need an audit trail.
func InsertDirect(
	ctx context.Context, q db.Querier,
	actor, action, resource string,
	statusCode int, metadata []byte,
) error {
	_, err := q.Exec(ctx, `
		INSERT INTO audit.events (actor, action, resource, status_code, metadata)
		VALUES ($1, $2, $3, $4, $5)`,
		actor, action, resource, statusCode, metadata,
	)
	if err != nil {
		return fmt.Errorf("audit.InsertDirect: %w", err)
	}
	return nil
}

// AnonymizeActor replaces actor with a placeholder across a user's audit
// events — used by the GDPR account-deletion flow. It goes through a database
// function because runtime roles hold no UPDATE on audit.events.
func AnonymizeActor(ctx context.Context, pool *pgxpool.Pool, authSub string) error {
	if _, err := pool.Exec(ctx, `SELECT audit.anonymize_actor($1)`, authSub); err != nil {
		return fmt.Errorf("audit.AnonymizeActor: %w", err)
	}
	return nil
}

// cleanup goes through a database function because runtime roles hold no
// DELETE on audit.events. The function also enforces the retention floor, and
// removes a bounded batch per call, so this repeats until nothing is left. The
// count it logs is the evidence that retention actually ran. A failure is counted
// because audit rows outliving their retention is a gap someone needs to hear about.
func (w *Writer) cleanup(ctx context.Context) {
	var total int64
	defer func() {
		if total > 0 {
			w.logger.Info("audit retention sweep removed rows", "rows", total)
		}
	}()
	for {
		batchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		var removed int64
		err := w.pool.QueryRow(batchCtx, `SELECT audit.sweep_expired($1)`, w.retentionDays).Scan(&removed)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			w.sweepFailures.Add(ctx, 1, metric.WithAttributes(attribute.String("module", "audit")))
			w.logger.Warn("audit cleanup failed", "error", err)
			return
		}
		total += removed
		if removed == 0 {
			return
		}
	}
}

func (w *Writer) flush(batch []event) {
	if len(batch) == 0 {
		return
	}

	var b strings.Builder
	b.WriteString("INSERT INTO audit.events (organization_id, actor, action, resource, status_code, metadata, ip, user_agent) VALUES ")

	args := make([]any, 0, len(batch)*8)
	for i, e := range batch {
		if i > 0 {
			b.WriteString(", ")
		}
		base := i * 8
		fmt.Fprintf(&b, "($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
			base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8)

		// One value Postgres rejects as JSON fails the whole multi-row
		// INSERT and loses every event in the batch, so anything that is not
		// a valid JSON object or array — a truncated or malformed request
		// body included — is stored as {} instead. So is a NUL escape, which
		// is valid JSON that jsonb refuses: it is the cheapest way to force
		// the retry below, and nothing drains the buffer while that retry runs.
		meta := e.metadata
		if !json.Valid(meta) || (meta[0] != '{' && meta[0] != '[') || bytes.Contains(meta, nulEscape) {
			meta = emptyMetadata
		}
		// A request can carry any bytes in this header, and Postgres refuses text
		// that is not valid UTF-8 or that holds a NUL.
		userAgent := strings.ReplaceAll(strings.ToValidUTF8(e.userAgent, "\uFFFD"), "\x00", "")
		args = append(args, e.organizationID, e.actor, e.action, e.resource, e.statusCode, meta, e.ip, userAgent)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := w.pool.Exec(ctx, b.String(), args...); err != nil {
		if w.flushAroundRefusedValue(batch, err) {
			return
		}
		w.writeFailures.Add(context.Background(), 1)
		w.logger.Warn("audit flush failed", "batch_size", len(batch), "error", err)
	}
}

// flushAroundRefusedValue handles a batch Postgres refused because of a value in
// it, and reports whether it did. A batch is one statement, so without this a caller
// with no account could erase everyone else's events with one crafted request every
// flush. json.Valid accepts bodies jsonb does not, such as one nested too deeply.
// So the batch is split in half until the bad row stands alone, which keeps one bad
// row among hundreds to a handful of statements, and that row is retried once with
// empty metadata, because the record that the request happened is worth keeping
// without its body. Any other failure is not the data's fault, and retrying each row
// would only multiply it.
func (w *Writer) flushAroundRefusedValue(batch []event, err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || !isRefusedValue(pgErr.Code) {
		return false
	}
	if len(batch) > 1 {
		mid := len(batch) / 2
		w.flush(batch[:mid])
		w.flush(batch[mid:])
		return true
	}
	if !bytes.Equal(batch[0].metadata, emptyMetadata) {
		batch[0].metadata = emptyMetadata
		w.flush(batch)
		return true
	}
	return false
}

// isRefusedValue reports a data exception (class 22) or a program limit such as a
// value nested too deeply (class 54).
func isRefusedValue(sqlState string) bool {
	return strings.HasPrefix(sqlState, "22") || strings.HasPrefix(sqlState, "54")
}

// Stop closes the channel and blocks until run has drained it and flushed
// the final batch — so a caller relying on the doc's promise (e.g. shutdown code
// closing the DB pool right after) will not race the final flush against the
// pool going away.
func (w *Writer) Stop() {
	w.stopCleanup()
	<-w.cleanupDone
	close(w.ch)
	<-w.done
}

func (w *Writer) enqueue(e event) {
	select {
	case w.ch <- e:
	default:
		// channel full — drop rather than block the request, and keep the
		// loss visible: a silent gap in an audit trail is worse than a noisy one
		w.droppedSinceLog.Add(1)
		w.dropped.Add(context.Background(), 1)
	}
}

// recordAuthFailure counts a 401 or 403. Callers run it before deciding whether
// the request deserves an audit row, so credential probing is counted on every
// method, including the GETs Middleware does not write a row for.
func (w *Writer) recordAuthFailure(c *gin.Context) {
	status := c.Writer.Status()
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		return
	}
	w.authFailures.Add(c.Request.Context(), 1, metric.WithAttributes(
		attribute.Int("status", status),
		attribute.String("route", c.FullPath()),
	))
}

// FailureMiddleware records only failed requests, with no body capture and
// empty metadata. It is meant for unauthenticated provider callbacks: a forged
// or misconfigured call leaves a security record, a successful one is already
// reflected in the state it changed, and neither body belongs in the audit log.
func (w *Writer) FailureMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		w.recordAuthFailure(c)
		if c.Writer.Status() < http.StatusBadRequest {
			return
		}
		w.enqueue(event{
			actor:      anonymousActor,
			action:     c.Request.Method,
			resource:   c.FullPath(),
			statusCode: c.Writer.Status(),
			metadata:   emptyMetadata,
			ip:         c.ClientIP(),
			userAgent:  c.Request.UserAgent(),
		})
	}
}

// Middleware returns a gin middleware that records mutating requests and
// auth failures as audit events. Handlers can set c.Set("audit.action", "...")
// to override the default HTTP method label or to force-audit a GET endpoint.
func (w *Writer) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		isGet := c.Request.Method == http.MethodGet || c.Request.Method == http.MethodOptions

		var reqBody []byte
		isMultipart := strings.HasPrefix(c.Request.Header.Get("Content-Type"), "multipart/")
		if !isGet && !isMultipart && c.Request.Body != nil {
			// Only the first maxBodyBytes are kept for the audit record. The
			// handler still reads the whole body: the copied head first, then
			// the rest straight from the original stream.
			orig := c.Request.Body
			reqBody, _ = io.ReadAll(io.LimitReader(orig, maxBodyBytes))
			c.Request.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(reqBody), orig), orig}
		}

		rbw := &responseBodyWriter{ResponseWriter: c.Writer, body: &bytes.Buffer{}}
		c.Writer = rbw

		c.Next()

		w.recordAuthFailure(c)

		// Handlers can set "audit.action" to override the default HTTP-method
		// label or to force-audit a GET; otherwise GETs are skipped entirely.
		action := c.GetString("audit.action")
		if isGet && action == "" {
			return
		}

		actor := anonymousActor
		if claims, ok := middleware.ClaimsFromContext(c); ok {
			actor = claims.Subject
		}

		// Skip anonymous requests that are not errors and not explicitly flagged.
		// This avoids recording probe/health traffic; 4xx from unauthenticated
		// requests (401 auth failures, 403 RBAC failures) are kept as security signals.
		if actor == anonymousActor && c.Writer.Status() < http.StatusBadRequest && action == "" {
			return
		}

		if action == "" {
			action = c.Request.Method
		}

		var organizationID *string
		if ws, ok := middleware.OrganizationFromContext(c); ok {
			organizationID = &ws.ID
		}

		if len(reqBody) == 0 {
			reqBody = []byte("{}")
		}
		// Only the audit copy is redacted — the handler already consumed the
		// real, unredacted body via c.Request.Body restored above. Routes
		// like acceptInvitation need the real token value to function.
		auditBody := redactSensitiveFields(reqBody)

		before, hasBefore := c.Get(auditBeforeKey)
		after, hasAfter := c.Get(auditAfterKey)

		meta := auditBody
		if hasBefore || hasAfter || (c.Writer.Status() >= http.StatusBadRequest && rbw.body.Len() > 0) {
			type metaShape struct {
				Before  any             `json:"before,omitempty"`
				After   any             `json:"after,omitempty"`
				Request json.RawMessage `json:"request,omitempty"`
				Error   json.RawMessage `json:"error,omitempty"`
			}
			ms := metaShape{}
			if hasBefore {
				ms.Before = before
			}
			if hasAfter {
				ms.After = after
			}
			req := json.RawMessage(auditBody)
			if !json.Valid(auditBody) {
				req = json.RawMessage(`{}`)
			}
			ms.Request = req
			if c.Writer.Status() >= http.StatusBadRequest && rbw.body.Len() > 0 {
				errBody := rbw.body.Bytes()
				errMsg := json.RawMessage(errBody)
				if !json.Valid(errBody) {
					quoted, _ := json.Marshal(string(errBody))
					errMsg = quoted
				}
				ms.Error = errMsg
			}
			if merged, err := json.Marshal(ms); err == nil {
				meta = merged
			}
		}

		w.enqueue(event{
			organizationID: organizationID,
			actor:          actor,
			action:         action,
			resource:       c.FullPath(),
			statusCode:     c.Writer.Status(),
			metadata:       meta,
			ip:             c.ClientIP(),
			userAgent:      c.Request.UserAgent(),
		})
	}
}

// Filter narrows an audit event query by the organization admin log's chip
// filters and date range. Every field is optional; a zero value means no
// constraint on that field. Actor is an exact match (indexed column).
// Action is an exact match against the raw HTTP-method value the middleware
// records — there's no semantic "member.*" category stored, so filtering
// reflects what the data actually contains. Resource is a partial ILIKE
// match against the route path.
type Filter struct {
	Actor    string
	Action   string
	Resource string
	From     *time.Time
	To       *time.Time
}

func (f Filter) apply(query string, args *db.Args) string {
	if f.Actor != "" {
		query += fmt.Sprintf(" AND actor = $%d", args.Add(f.Actor))
	}
	if f.Action != "" {
		query += fmt.Sprintf(" AND action = $%d", args.Add(f.Action))
	}
	if f.Resource != "" {
		query += fmt.Sprintf(" AND resource ILIKE $%d", args.Add("%"+f.Resource+"%"))
	}
	if f.From != nil {
		query += fmt.Sprintf(" AND created_at >= $%d", args.Add(*f.From))
	}
	if f.To != nil {
		query += fmt.Sprintf(" AND created_at <= $%d", args.Add(*f.To))
	}
	return query
}

// ListByOrganization returns audit events for an organization, newest first,
// optionally narrowed by filter.
func ListByOrganization(
	ctx context.Context, pool *pgxpool.Pool,
	organizationID string, limit, offset int, filter Filter,
) ([]EventRecord, int64, error) {
	countQuery := `SELECT COUNT(*) FROM audit.events WHERE organization_id = $1`
	countArgs := db.NewArgs(organizationID)
	countQuery = filter.apply(countQuery, countArgs)
	var total int64
	if err := pool.QueryRow(ctx, countQuery, countArgs.Values()...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("audit.ListByOrganization: count: %w", err)
	}

	query := `SELECT id, organization_id, actor, action, resource, status_code, metadata, ip, user_agent, created_at
		FROM audit.events WHERE organization_id = $1`
	args := db.NewArgs(organizationID)
	query = filter.apply(query, args)
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", args.Add(limit), args.Add(offset))

	rows, err := pool.Query(ctx, query, args.Values()...)
	if err != nil {
		return nil, 0, fmt.Errorf("audit.ListByOrganization: %w", err)
	}
	defer rows.Close()

	var out []EventRecord
	for rows.Next() {
		var e EventRecord
		if err := rows.Scan(&e.ID, &e.OrganizationID, &e.Actor, &e.Action, &e.Resource,
			&e.StatusCode, &e.Metadata, &e.IP, &e.UserAgent, &e.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("audit.ListByOrganization: scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("audit.ListByOrganization: %w", err)
	}
	return out, total, nil
}

// ListByOrganizationCursor returns audit events for an organization using cursor-based pagination,
// newest first, optionally narrowed by filter. Pass an empty cursor to get
// the first page. Returns events and the next cursor (empty when there are
// no more pages).
func ListByOrganizationCursor(
	ctx context.Context, pool *pgxpool.Pool,
	organizationID string, limit int, cursor string, filter Filter,
) ([]EventRecord, string, error) {
	query := `SELECT id, organization_id, actor, action, resource, status_code, metadata, ip, user_agent, created_at
		FROM audit.events WHERE organization_id = $1`
	args := db.NewArgs(organizationID)

	query = filter.apply(query, args)

	if cursor != "" {
		parts := strings.SplitN(cursor, "_", 2)
		if len(parts) == 2 {
			ts, err := time.Parse(time.RFC3339Nano, parts[0])
			if err != nil {
				return nil, "", fmt.Errorf("audit.ListByOrganizationCursor: invalid cursor timestamp: %w", err)
			}
			query += fmt.Sprintf(" AND (created_at, id) < ($%d, $%d::uuid)", args.Add(ts), args.Add(parts[1]))
		} else {
			return nil, "", fmt.Errorf("audit.ListByOrganizationCursor: invalid cursor format")
		}
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", args.Add(limit))

	rows, err := pool.Query(ctx, query, args.Values()...)
	if err != nil {
		return nil, "", fmt.Errorf("audit.ListByOrganizationCursor: %w", err)
	}
	defer rows.Close()

	out := make([]EventRecord, 0, limit)
	for rows.Next() {
		var e EventRecord
		if err := rows.Scan(
			&e.ID, &e.OrganizationID, &e.Actor, &e.Action, &e.Resource,
			&e.StatusCode, &e.Metadata, &e.IP, &e.UserAgent, &e.CreatedAt,
		); err != nil {
			return nil, "", fmt.Errorf("audit.ListByOrganizationCursor: scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("audit.ListByOrganizationCursor: %w", err)
	}

	nextCursor := ""
	if len(out) == limit {
		nextCursor = fmt.Sprintf("%s_%s", out[len(out)-1].CreatedAt.Format(time.RFC3339Nano), out[len(out)-1].ID)
	}
	return out, nextCursor, nil
}

// ListByActorCursor returns audit events across every organization for a single
// actor using cursor-based pagination, newest first — a personal "what did I
// do" trail rather than a per-organization admin view. Pass an empty cursor to
// get the first page. Optional from/to bound created_at the same way
// ExportByActor's do (inclusive), so the visible list and the CSV export can
// be scoped to the same range. Returns events and the next cursor (empty when
// there are no more pages).
//
// Unlike ListByOrganizationCursor, this paginates by created_at rather than id:
// audit.events.id is gen_random_uuid() (random v4, not time-ordered), so
// "ORDER BY id DESC" does not actually mean newest-first.
func ListByActorCursor(
	ctx context.Context, pool *pgxpool.Pool,
	authSub string, limit int, cursor string, from, to *time.Time,
) ([]EventRecord, string, error) {
	query := `SELECT id, organization_id, actor, action, resource, status_code, metadata, ip, user_agent, created_at
		FROM audit.events WHERE actor = $1`
	args := db.NewArgs(authSub)

	if from != nil {
		query += fmt.Sprintf(" AND created_at >= $%d", args.Add(*from))
	}
	if to != nil {
		query += fmt.Sprintf(" AND created_at <= $%d", args.Add(*to))
	}

	if cursor != "" {
		ts, err := time.Parse(time.RFC3339Nano, cursor)
		if err != nil {
			return nil, "", fmt.Errorf("audit.ListByActorCursor: invalid cursor: %w", err)
		}
		query += fmt.Sprintf(" AND created_at < $%d", args.Add(ts))
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", args.Add(limit))

	rows, err := pool.Query(ctx, query, args.Values()...)
	if err != nil {
		return nil, "", fmt.Errorf("audit.ListByActorCursor: %w", err)
	}
	defer rows.Close()

	out := make([]EventRecord, 0, limit)
	for rows.Next() {
		var e EventRecord
		if err := rows.Scan(
			&e.ID, &e.OrganizationID, &e.Actor, &e.Action, &e.Resource,
			&e.StatusCode, &e.Metadata, &e.IP, &e.UserAgent, &e.CreatedAt,
		); err != nil {
			return nil, "", fmt.Errorf("audit.ListByActorCursor: scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("audit.ListByActorCursor: %w", err)
	}

	nextCursor := ""
	if len(out) == limit {
		// Cursor keys on created_at alone, not (created_at, id) — two events
		// landing in the same nanosecond could skip/duplicate one row at a
		// page boundary. Fine for a personal audit trail; use a composite
		// keyset cursor if this ever needs stronger pagination guarantees.
		nextCursor = out[len(out)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	return out, nextCursor, nil
}

// csvSafe prefixes a cell with a single quote if it starts with a character
// (=, +, -, @, tab, or carriage return) that Excel/Sheets treats as the
// start of a formula when the cell is opened — otherwise an attacker who
// controls a field written into these exports (user_agent is fully
// attacker-controlled; resource/actor can embed user-supplied names) could
// get arbitrary formula execution in whatever spreadsheet app opens the
// export. The prefix defeats formula parsing while leaving the value
// otherwise readable.
func csvSafe(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	default:
		return s
	}
}

// ExportByActor streams a single actor's audit events, across every
// organization, as CSV to w. Optional from/to filter by created_at (inclusive).
// Unlike ExportByOrganization, organization_id can be NULL here (e.g. /me routes
// aren't scoped to an organization) and the actor column is omitted — it's
// always the same value, so it'd be redundant on every row.
func ExportByActor(
	ctx context.Context, pool *pgxpool.Pool,
	authSub string, from, to *time.Time, w io.Writer,
) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write([]string{
		"id", "organization_id", "action", "resource",
		"status_code", "ip", "user_agent", "created_at",
	}); err != nil {
		return fmt.Errorf("audit.ExportByActor: write header: %w", err)
	}

	query := `SELECT id, organization_id, action, resource, status_code, ip, user_agent, created_at FROM audit.events WHERE actor = $1`
	args := db.NewArgs(authSub)
	if from != nil {
		query += fmt.Sprintf(" AND created_at >= $%d", args.Add(*from))
	}
	if to != nil {
		query += fmt.Sprintf(" AND created_at <= $%d", args.Add(*to))
	}
	query += " ORDER BY created_at DESC"

	rows, err := pool.Query(ctx, query, args.Values()...)
	if err != nil {
		return fmt.Errorf("audit.ExportByActor: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, action, resource, ip, ua string
		var orgID *string
		var code int
		var ts time.Time
		if err := rows.Scan(&id, &orgID, &action, &resource, &code, &ip, &ua, &ts); err != nil {
			return fmt.Errorf("audit.ExportByActor: scan: %w", err)
		}
		orgCol := ""
		if orgID != nil {
			orgCol = *orgID
		}
		if err := cw.Write([]string{id, orgCol, csvSafe(action), csvSafe(resource), strconv.Itoa(code), ip, csvSafe(ua), ts.Format(time.RFC3339)}); err != nil {
			return fmt.Errorf("audit.ExportByActor: write row: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("audit.ExportByActor: %w", err)
	}
	if err := cw.Error(); err != nil {
		return fmt.Errorf("audit.ExportByActor: %w", err)
	}
	return nil
}

// ExportByOrganization streams audit events for an organization as CSV to
// w, honoring the same filter as ListByOrganizationCursor so the export
// matches whatever the admin currently has on screen.
func ExportByOrganization(
	ctx context.Context, pool *pgxpool.Pool,
	organizationID string, filter Filter, w io.Writer,
) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write([]string{
		"id", "organization_id", "actor", "action", "resource",
		"status_code", "ip", "user_agent", "created_at",
	}); err != nil {
		return fmt.Errorf("audit.ExportByOrganization: write header: %w", err)
	}

	query := `SELECT id, organization_id, actor, action, resource, status_code, ip, user_agent, created_at FROM audit.events WHERE organization_id = $1`
	args := db.NewArgs(organizationID)
	query = filter.apply(query, args)
	query += " ORDER BY created_at DESC"

	rows, err := pool.Query(ctx, query, args.Values()...)
	if err != nil {
		return fmt.Errorf("audit.ExportByOrganization: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, orgID, actor, action, resource, ip, ua string
		var code int
		var ts time.Time
		if err := rows.Scan(&id, &orgID, &actor, &action, &resource, &code, &ip, &ua, &ts); err != nil {
			return fmt.Errorf("audit.ExportByOrganization: scan: %w", err)
		}
		if err := cw.Write([]string{
			id, orgID, csvSafe(actor), csvSafe(action), csvSafe(resource),
			strconv.Itoa(code), ip, csvSafe(ua), ts.Format(time.RFC3339),
		}); err != nil {
			return fmt.Errorf("audit.ExportByOrganization: write row: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("audit.ExportByOrganization: %w", err)
	}
	if err := cw.Error(); err != nil {
		return fmt.Errorf("audit.ExportByOrganization: %w", err)
	}
	return nil
}
