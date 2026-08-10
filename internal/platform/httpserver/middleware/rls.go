package middleware

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/db"
)

const (
	// responseBufferCap bounds bufferedWriter's in-memory buffer per request.
	// Set comfortably above the largest measured billing RLS-group response —
	// a 500-line-item invoice PDF (already an unrealistic invoice size) came
	// in at ~43KB, text-only (no embedded images) — with wide headroom for
	// growth, while still bounding worst-case memory instead of leaving it
	// unbounded.
	responseBufferCap = 5 << 20 // 5MB
	// responseBufferWarnThreshold logs once a response gets uncomfortably
	// close to the cap, so growth toward the ceiling is visible before it
	// becomes a production failure.
	responseBufferWarnThreshold = responseBufferCap / 2
)

var errResponseBufferCapExceeded = errors.New("rls: buffered response exceeds cap")

// bufferedWriter captures status + body WITHOUT writing through to the client,
// so the transaction can be committed before anything is flushed. Buffering
// is capped at responseBufferCap: once a response would exceed it, Write
// stops accumulating further bytes (so memory use stays bounded) and returns
// errResponseBufferCapExceeded; NewRLSTxMiddleware checks overCap itself
// after the handler returns, so the request fails the transaction and
// returns an error regardless of whether the handler's own serializer
// happened to check Write's return value.
type bufferedWriter struct {
	gin.ResponseWriter
	body    bytes.Buffer
	status  int
	overCap bool
	warned  bool
}

func (w *bufferedWriter) Write(b []byte) (int, error) {
	if w.overCap {
		return 0, errResponseBufferCapExceeded
	}
	if w.body.Len()+len(b) > responseBufferCap {
		w.overCap = true
		return 0, errResponseBufferCapExceeded
	}
	if !w.warned && w.body.Len()+len(b) > responseBufferWarnThreshold {
		w.warned = true
		slog.Warn("rls: buffered response approaching cap",
			"bytes", w.body.Len()+len(b), "cap", responseBufferCap)
	}
	return w.body.Write(b)
}

func (w *bufferedWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *bufferedWriter) WriteHeader(status int)            { w.status = status }
func (w *bufferedWriter) Status() int                       { return w.status }

// NewRLSTxMiddleware wraps each request in a transaction and executes
// SET LOCAL app.organization_id = '<id>' so that RLS policies on billing
// tables can enforce organization isolation at the database level.
//
// The transaction is committed on 2xx responses and rolled back on
// 4xx/5xx. Only apply this to routes that use the organization middleware,
// since it reads OrganizationFromContext.
func NewRLSTxMiddleware(pool *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		ws, ok := OrganizationFromContext(c)
		if !ok {
			c.Next()
			return
		}

		ctx := db.WithPendingEvents(c.Request.Context())
		tx, err := pool.Begin(ctx)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "database error"})
			return
		}

		if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id', $1, true)`, ws.ID); err != nil {
			_ = tx.Rollback(ctx)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "database error"})
			return
		}

		// A panic in a downstream handler unwinds straight past the
		// commit/rollback below (it isn't in a defer) to the outer
		// Recovery middleware, leaving this transaction — and its pooled
		// connection — neither committed nor rolled back. This defer
		// guarantees the rollback still runs, then re-panics so Recovery
		// still produces the 500 response.
		originalWriter := c.Writer
		bw := &bufferedWriter{ResponseWriter: originalWriter, status: http.StatusOK}
		c.Writer = bw
		c.Request = c.Request.WithContext(db.WithQuerier(ctx, tx))

		defer func() {
			c.Writer = originalWriter
			if r := recover(); r != nil {
				_ = tx.Rollback(ctx)
				panic(r)
			}
		}()

		c.Next()

		c.Writer = originalWriter

		if bw.overCap {
			_ = tx.Rollback(ctx)
			slog.ErrorContext(ctx, "rls: response exceeded buffer cap, rolling back", "cap", responseBufferCap)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "response too large"})
			return
		}

		flush := func() {
			originalWriter.WriteHeader(bw.status)
			_, _ = originalWriter.Write(bw.body.Bytes())
		}

		if bw.status >= http.StatusBadRequest {
			_ = tx.Rollback(ctx)
			flush()
			return
		}

		if err := tx.Commit(ctx); err != nil {
			slog.ErrorContext(ctx, "rls: commit failed, discarding buffered response", "error", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "database error"})
			return
		}

		// Events a service method queued via db.QueueEvent (instead of
		// publishing immediately) only fire once the transaction that
		// produced their state is durably committed.
		db.FlushPendingEvents(ctx)
		flush()
	}
}
