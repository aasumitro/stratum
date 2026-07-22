package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/db"
)

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
		defer func() {
			if r := recover(); r != nil {
				_ = tx.Rollback(ctx)
				panic(r)
			}
		}()

		c.Request = c.Request.WithContext(db.WithQuerier(ctx, tx))
		c.Next()

		if c.Writer.Status() >= http.StatusBadRequest {
			_ = tx.Rollback(ctx)
		} else if err := tx.Commit(ctx); err == nil {
			// Events a service method queued via db.QueueEvent (instead of
			// publishing immediately) only fire once the transaction that
			// produced their state is durably committed.
			db.FlushPendingEvents(ctx)
		}
	}
}
