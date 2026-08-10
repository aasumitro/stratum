package notification

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"

	"github.com/aasumitro/stratum/internal/platform/httpserver/reqctx"
	"github.com/aasumitro/stratum/internal/platform/httpserver/request"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

// maxConcurrentSSEStreamsPerSubject caps how many notification streams one
// caller can hold open at once — the per-minute rate limiter on this route
// only throttles how often a stream can be *opened*, not how many stay open
// concurrently, so without this a client could accumulate an unbounded
// number of long-lived Redis subscriptions and goroutines.
const maxConcurrentSSEStreamsPerSubject = 3

// sseSlotTTL and sseSlotRefreshInterval are vars, not consts, so tests can
// shrink them (see export_test.go) and observe a refresh without waiting
// out the real multi-minute cadence.
var (
	// sseSlotTTL bounds how long an open stream's counter slot survives
	// without a refresh — see the TTL refresh in streamNotifications below
	// for why this needs to keep being pushed out for the life of a
	// long-lived connection.
	sseSlotTTL = 10 * time.Minute
	// sseSlotRefreshInterval keeps wide margin under sseSlotTTL so a slow
	// tick or a brief Redis hiccup can't let the slot expire while the
	// stream is still open.
	sseSlotRefreshInterval = 2 * time.Minute
)

type handler struct {
	svc *service
}

// sseOpenKey is the Redis counter key tracking how many notification
// streams a subject currently has open — shared between
// ConcurrentSSELimitMiddleware (which increments/decrements it) and
// streamNotifications (which periodically refreshes its TTL) so both stay
// in sync on the exact same key.
func sseOpenKey(subject string) string {
	return "sse:open:" + subject
}

// ConcurrentSSELimitMiddleware rejects a stream request once the caller
// already has max notification streams open, tracked via a Redis counter
// keyed per subject. Only mounted on routes registered with a non-nil Redis
// client (see module.go), so a Redis error here is an infra hiccup, not a
// missing dependency — fails open (lets the request through unmetered)
// rather than blocking streaming over it.
func ConcurrentSSELimitMiddleware(redisClient *goredis.Client, maxStreams int) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := sseOpenKey(reqctx.Subject(c))
		n, err := redisClient.Incr(c.Request.Context(), key).Result()
		if err != nil {
			c.Next()
			return
		}
		// TTL is a safety net only, in case a connection ever ends without
		// the deferred Decr running (e.g. the process is killed mid-stream).
		// streamNotifications refreshes this same TTL periodically for as
		// long as the stream stays open, so a legitimately long-lived
		// stream never has its slot expire out from under it.
		redisClient.Expire(c.Request.Context(), key, sseSlotTTL)
		if n > int64(maxStreams) {
			redisClient.Decr(c.Request.Context(), key)
			response.Error("TOO_MANY_STREAMS", "too many concurrent notification streams open").JSON(c, http.StatusTooManyRequests)
			c.Abort()
			return
		}
		defer redisClient.Decr(context.WithoutCancel(c.Request.Context()), key)
		c.Next()
	}
}

// listNotifications godoc
// @Summary      List notifications
// @Description  Returns the caller's notifications. Cursor pagination (response carries next_cursor, no total) once a cursor is supplied, or by default when neither cursor nor page is given and another page exists. Sending an explicit page forces page-based pagination (response carries total) even on a full page.
// @Tags         notification
// @Produce      json
// @Security     BearerAuth
// @Param        organization_id  query     string  false  "filter by organization"
// @Param        channel          query     string  false  "filter by channel"
// @Param        limit            query     int     false  "max results (default 20, max 100)"
// @Param        page             query     int     false  "page number; sending this forces page-based pagination (default 1)"
// @Param        cursor           query     string  false  "pagination cursor from a previous response; switches to cursor mode"
// @Success      200              {object}  response.Payload{data=[]messageRecord}
// @Failure      401              {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/notifications [get]
func (h *handler) listNotifications(c *gin.Context) {
	limit := queryIntClamped(c, "limit", 20, 100)
	cursor := c.Query("cursor")
	pageRequested := c.Query("page") != ""

	var offset int
	if cursor == "" {
		page := queryInt(c, "page", 1)
		offset = (page - 1) * limit
	}

	result, err := h.svc.listForUser(
		c.Request.Context(),
		reqctx.Subject(c),
		c.Query("organization_id"),
		c.Query("channel"),
		cursor,
		limit,
		offset,
		pageRequested,
	)
	if err != nil {
		response.FromError(c, err)
		return
	}
	if result.CursorMode {
		response.CursorList(result.Messages, result.NextCursor).JSON(c, http.StatusOK)
		return
	}
	response.List(result.Messages, result.Total).JSON(c, http.StatusOK)
}

// streamNotifications godoc
// @Summary      Stream notifications (SSE)
// @Description  Server-sent events stream; emits a "notification" event whenever a new message arrives for the caller. Only mounted when Redis is configured. Capped at 3 concurrent streams per caller.
// @Tags         notification
// @Produce      text/event-stream
// @Security     BearerAuth
// @Success      200  "text/event-stream connection"
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Failure      429  {object}  response.Payload  "too many concurrent streams already open"
// @Router       /me/notifications/stream [get]
func (h *handler) streamNotifications(c *gin.Context) {
	subject := reqctx.Subject(c)

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	ctx := c.Request.Context()
	sub := h.svc.redis.Subscribe(ctx, "notif:"+subject)
	defer sub.Close()

	// ConcurrentSSELimitMiddleware sets this same key's TTL once at open;
	// without a periodic refresh here, a stream held open longer than that
	// TTL would have its counter slot expire while still active, letting the
	// concurrency limit be bypassed and risking a negative counter when this
	// stream eventually closes and its own deferred Decr still runs.
	slotKey := sseOpenKey(subject)
	ticker := time.NewTicker(sseSlotRefreshInterval)
	defer ticker.Stop()

	ch := sub.Channel()
	c.Stream(func(_ io.Writer) bool {
		select {
		case _, ok := <-ch:
			if !ok {
				return false
			}
			c.SSEvent("notification", gin.H{"type": "new"})
			return true
		case <-ticker.C:
			h.svc.redis.Expire(ctx, slotKey, sseSlotTTL)
			return true
		case <-ctx.Done():
			return false
		}
	})
}

// unreadCount godoc
// @Summary      Get unread notification count
// @Description  Returns the number of unread notifications for the caller, optionally scoped to an organization.
// @Tags         notification
// @Produce      json
// @Security     BearerAuth
// @Param        organization_id  query     string  false  "filter by organization"
// @Success      200              {object}  response.Payload{data=object{unread=integer}}
// @Failure      401              {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/notifications/unread-count [get]
func (h *handler) unreadCount(c *gin.Context) {
	n, err := h.svc.unreadCount(c.Request.Context(), reqctx.Subject(c), c.Query("organization_id"))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(gin.H{"unread": n}).JSON(c, http.StatusOK)
}

// markRead godoc
// @Summary      Mark a notification as read
// @Tags         notification
// @Security     BearerAuth
// @Param        id  path  string  true  "Notification ID"
// @Success      204  "no content"
// @Failure      404  {object}  response.Payload  "notification not found"
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/notifications/{id}/read [patch]
func (h *handler) markRead(c *gin.Context) {
	if err := h.svc.markRead(c.Request.Context(), reqctx.Subject(c), c.Param("id")); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// markAllRead godoc
// @Summary      Mark all notifications as read
// @Tags         notification
// @Security     BearerAuth
// @Param        organization_id  query  string  false  "scope to an organization"
// @Success      204              "no content"
// @Failure      401              {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/notifications/read-all [patch]
func (h *handler) markAllRead(c *gin.Context) {
	if err := h.svc.markAllRead(c.Request.Context(), reqctx.Subject(c), c.Query("organization_id")); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// listPreferences godoc
// @Summary      List notification preferences
// @Description  Returns the caller's per-channel/event-type notification preferences.
// @Tags         notification
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.Payload{data=[]preferenceRecord}
// @Failure      401  {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/notifications/preferences [get]
func (h *handler) listPreferences(c *gin.Context) {
	prefs, err := h.svc.listPreferences(c.Request.Context(), reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(prefs).JSON(c, http.StatusOK)
}

type upsertPrefRequest struct {
	Channel   string `json:"channel"    binding:"required"`
	EventType string `json:"event_type" binding:"required"`
	Enabled   bool   `json:"enabled"`
}

// upsertPreference godoc
// @Summary      Set a notification preference
// @Description  Creates or updates the caller's preference for a given channel + event type.
// @Tags         notification
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      upsertPrefRequest                       true  "Preference to set"
// @Success      200   {object}  response.Payload{data=preferenceRecord}
// @Failure      422   {object}  response.Payload  "validation failed"
// @Failure      401   {object}  response.Payload  "missing/invalid auth token"
// @Router       /me/notifications/preferences [patch]
func (h *handler) upsertPreference(c *gin.Context) {
	var req upsertPrefRequest
	if !request.Bind(c, &req) {
		return
	}
	pref, err := h.svc.upsertPreference(c.Request.Context(), reqctx.Subject(c), req.Channel, req.EventType, req.Enabled)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(pref).JSON(c, http.StatusOK)
}

func queryInt(c *gin.Context, key string, fallback int) int {
	if v, err := strconv.Atoi(c.Query(key)); err == nil && v > 0 {
		return v
	}
	return fallback
}

// queryIntClamped is queryInt with an upper bound — unlike a plain positive
// int, this caps how large a single request can push a LIMIT/OFFSET-backed
// query (e.g. ?limit=100000000 forcing a huge scan and result
// materialization), matching the clamp account's session/audit-log list
// endpoints already apply.
func queryIntClamped(c *gin.Context, key string, fallback, maxVal int) int {
	if v := queryInt(c, key, fallback); v <= maxVal {
		return v
	}
	return maxVal
}
