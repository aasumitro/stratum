package audit

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/db"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
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

	auditBeforeKey = "audit.before"
	auditAfterKey  = "audit.after"
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
}

// NewWriter starts a background goroutine that drains the channel
// and writes batches to the database. Call Stop to flush remaining events.
func NewWriter(pool *pgxpool.Pool, logger *slog.Logger, retentionDays int) *Writer {
	w := &Writer{
		pool:          pool,
		ch:            make(chan event, bufferSize),
		logger:        logger,
		retentionDays: retentionDays,
		done:          make(chan struct{}),
	}
	go w.run()
	return w
}

func (w *Writer) run() {
	defer close(w.done)
	batch := make([]event, 0, batchSize)
	flushTicker := time.NewTicker(flushInterval)
	cleanupTicker := time.NewTicker(cleanupTick)
	defer flushTicker.Stop()
	defer cleanupTicker.Stop()

	for {
		select {
		case e, ok := <-w.ch:
			if !ok {
				if len(batch) > 0 {
					w.flush(batch)
				}
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
		case <-cleanupTicker.C:
			w.cleanup()
		}
	}
}

// InsertDirect records a single audit event immediately, bypassing the
// batching Writer. For call sites outside the /api/v1 middleware chain
// (e.g. webhook routes) that have no request to hang Middleware off of but
// still need an audit trail.
func InsertDirect(ctx context.Context, q db.Querier, actor, action, resource string, statusCode int, metadata []byte) error {
	_, err := q.Exec(ctx, `
		INSERT INTO audit.events (actor, action, resource, status_code, metadata)
		VALUES ($1, $2, $3, $4, $5)`,
		actor, action, resource, statusCode, metadata,
	)
	return err
}

// AnonymizeActor replaces actor with a placeholder across a user's audit
// events — used by the GDPR account-deletion flow.
func AnonymizeActor(ctx context.Context, pool *pgxpool.Pool, authSub string) error {
	_, err := pool.Exec(ctx, `UPDATE audit.events SET actor = 'deleted_user' WHERE actor = $1`, authSub)
	return err
}

func (w *Writer) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cutoff := time.Now().AddDate(0, 0, -w.retentionDays)
	if _, err := w.pool.Exec(ctx, `DELETE FROM audit.events WHERE created_at < $1`, cutoff); err != nil {
		w.logger.Warn("audit cleanup failed", "error", err)
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

		meta := e.metadata
		if len(meta) == 0 || (meta[0] != '{' && meta[0] != '[') {
			meta = []byte("{}")
		}
		args = append(args, e.organizationID, e.actor, e.action, e.resource, e.statusCode, meta, e.ip, e.userAgent)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := w.pool.Exec(ctx, b.String(), args...); err != nil {
		w.logger.Warn("audit flush failed", "batch_size", len(batch), "error", err)
	}
}

// Stop closes the channel and blocks until run has drained it and flushed
// the final batch — previously this only closed the channel and returned
// immediately, so a caller relying on the doc's promise (e.g. shutdown code
// closing the DB pool right after) could race the final flush against the
// pool going away.
func (w *Writer) Stop() {
	close(w.ch)
	<-w.done
}

func (w *Writer) enqueue(e event) {
	select {
	case w.ch <- e:
	default:
		// channel full — drop rather than block the request
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
			reqBody, _ = io.ReadAll(io.LimitReader(c.Request.Body, maxBodyBytes))
			c.Request.Body = io.NopCloser(bytes.NewReader(reqBody))
		}

		rbw := &responseBodyWriter{ResponseWriter: c.Writer, body: &bytes.Buffer{}}
		c.Writer = rbw

		c.Next()

		// Handlers can set "audit.action" to override the default HTTP-method
		// label or to force-audit a GET; otherwise GETs are skipped entirely.
		action := c.GetString("audit.action")
		if isGet && action == "" {
			return
		}

		actor := "anonymous"
		if claims, ok := middleware.ClaimsFromContext(c); ok {
			actor = claims.Subject
		}

		// Skip anonymous requests that are not errors and not explicitly flagged.
		// This avoids recording probe/health traffic; 4xx from unauthenticated
		// requests (401 auth failures, 403 RBAC failures) are kept as security signals.
		if actor == "anonymous" && c.Writer.Status() < http.StatusBadRequest && action == "" {
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
func ListByOrganization(ctx context.Context, pool *pgxpool.Pool, organizationID string, limit, offset int, filter Filter) ([]EventRecord, int64, error) {
	countQuery := `SELECT COUNT(*) FROM audit.events WHERE organization_id = $1`
	countArgs := db.NewArgs(organizationID)
	countQuery = filter.apply(countQuery, countArgs)
	var total int64
	if err := pool.QueryRow(ctx, countQuery, countArgs.Values()...).Scan(&total); err != nil {
		return nil, 0, err
	}

	query := `SELECT id, organization_id, actor, action, resource, status_code, metadata, ip, user_agent, created_at
		FROM audit.events WHERE organization_id = $1`
	args := db.NewArgs(organizationID)
	query = filter.apply(query, args)
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", args.Add(limit), args.Add(offset))

	rows, err := pool.Query(ctx, query, args.Values()...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []EventRecord
	for rows.Next() {
		var e EventRecord
		if err := rows.Scan(&e.ID, &e.OrganizationID, &e.Actor, &e.Action, &e.Resource,
			&e.StatusCode, &e.Metadata, &e.IP, &e.UserAgent, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// ListByOrganizationCursor returns audit events for an organization using cursor-based pagination,
// newest first, optionally narrowed by filter. Pass an empty cursor to get
// the first page. Returns events and the next cursor (empty when there are
// no more pages).
func ListByOrganizationCursor(ctx context.Context, pool *pgxpool.Pool, organizationID string, limit int, cursor string, filter Filter) ([]EventRecord, string, error) {
	query := `SELECT id, organization_id, actor, action, resource, status_code, metadata, ip, user_agent, created_at
		FROM audit.events WHERE organization_id = $1`
	args := db.NewArgs(organizationID)

	query = filter.apply(query, args)

	if cursor != "" {
		query += fmt.Sprintf(" AND id < $%d", args.Add(cursor))
	}

	query += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", args.Add(limit))

	rows, err := pool.Query(ctx, query, args.Values()...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	out := make([]EventRecord, 0, limit)
	for rows.Next() {
		var e EventRecord
		if err := rows.Scan(
			&e.ID, &e.OrganizationID, &e.Actor, &e.Action, &e.Resource,
			&e.StatusCode, &e.Metadata, &e.IP, &e.UserAgent, &e.CreatedAt,
		); err != nil {
			return nil, "", err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	nextCursor := ""
	if len(out) == limit {
		nextCursor = out[len(out)-1].ID
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
func ListByActorCursor(ctx context.Context, pool *pgxpool.Pool, authSub string, limit int, cursor string, from, to *time.Time) ([]EventRecord, string, error) {
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
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		query += fmt.Sprintf(" AND created_at < $%d", args.Add(ts))
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", args.Add(limit))

	rows, err := pool.Query(ctx, query, args.Values()...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	out := make([]EventRecord, 0, limit)
	for rows.Next() {
		var e EventRecord
		if err := rows.Scan(
			&e.ID, &e.OrganizationID, &e.Actor, &e.Action, &e.Resource,
			&e.StatusCode, &e.Metadata, &e.IP, &e.UserAgent, &e.CreatedAt,
		); err != nil {
			return nil, "", err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	nextCursor := ""
	if len(out) == limit {
		// ponytail: cursor keys on created_at alone, not (created_at, id) — two
		// events landing in the same nanosecond could skip/duplicate one row at
		// a page boundary. Fine for a personal audit trail; use a composite
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
func ExportByActor(ctx context.Context, pool *pgxpool.Pool, authSub string, from, to *time.Time, w io.Writer) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write([]string{"id", "organization_id", "action", "resource", "status_code", "ip", "user_agent", "created_at"}); err != nil {
		return err
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
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id, action, resource, ip, ua string
		var orgID *string
		var code int
		var ts time.Time
		if err := rows.Scan(&id, &orgID, &action, &resource, &code, &ip, &ua, &ts); err != nil {
			return err
		}
		orgCol := ""
		if orgID != nil {
			orgCol = *orgID
		}
		if err := cw.Write([]string{id, orgCol, csvSafe(action), csvSafe(resource), strconv.Itoa(code), ip, csvSafe(ua), ts.Format(time.RFC3339)}); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return cw.Error()
}

// ExportByOrganization streams audit events for an organization as CSV to
// w, honoring the same filter as ListByOrganizationCursor so the export
// matches whatever the admin currently has on screen.
func ExportByOrganization(ctx context.Context, pool *pgxpool.Pool, organizationID string, filter Filter, w io.Writer) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write([]string{"id", "organization_id", "actor", "action", "resource", "status_code", "ip", "user_agent", "created_at"}); err != nil {
		return err
	}

	query := `SELECT id, organization_id, actor, action, resource, status_code, ip, user_agent, created_at FROM audit.events WHERE organization_id = $1`
	args := db.NewArgs(organizationID)
	query = filter.apply(query, args)
	query += " ORDER BY created_at DESC"

	rows, err := pool.Query(ctx, query, args.Values()...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id, orgID, actor, action, resource, ip, ua string
		var code int
		var ts time.Time
		if err := rows.Scan(&id, &orgID, &actor, &action, &resource, &code, &ip, &ua, &ts); err != nil {
			return err
		}
		if err := cw.Write([]string{id, orgID, csvSafe(actor), csvSafe(action), csvSafe(resource), strconv.Itoa(code), ip, csvSafe(ua), ts.Format(time.RFC3339)}); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return cw.Error()
}
