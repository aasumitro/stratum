package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/cache"
)

// idempotencyLockTTL bounds how long a concurrent duplicate request waits
// to be rejected before the in-flight marker is considered stale (e.g. the
// original request's process crashed mid-handler). Long enough to cover a
// normal billing request, short enough that a crash doesn't block retries
// for long.
const idempotencyLockTTL = 30 * time.Second

// idempotentWriter captures the status code and body written by downstream handlers.
type idempotentWriter struct {
	gin.ResponseWriter
	body   bytes.Buffer
	status int
}

func (w *idempotentWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *idempotentWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

type idempotentCached struct {
	Status   int    `json:"s"`
	Body     []byte `json:"b"`
	BodyHash string `json:"h"`
}

// idempotencyCacheKey scopes a client-supplied Idempotency-Key to the
// caller and route, so two different callers (or two different endpoints)
// can never collide on the same raw key value and replay each other's
// cached response.
func idempotencyCacheKey(subject, method, path, rawKey string) string {
	return subject + ":" + method + ":" + path + ":" + rawKey
}

// NewIdempotencyMiddleware returns a middleware that replays cached responses
// for requests carrying an Idempotency-Key header. The cache key is scoped
// to subject+method+path (not just the raw header value) so two callers
// reusing the same key value can't see each other's response. A request
// body hash is cached alongside the response so a same-key-different-body
// replay gets a 409 instead of silently returning the first request's
// answer. A short-lived in-flight marker rejects a concurrent duplicate
// instead of letting both execute. Only 2xx responses are cached; the
// cache TTL is 24 hours.
func NewIdempotencyMiddleware(ns *cache.Namespace) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawKey := c.GetHeader("Idempotency-Key")
		if rawKey == "" {
			c.Next()
			return
		}

		ctx := c.Request.Context()

		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "cannot read body"})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		bodyHashRaw := sha256.Sum256(bodyBytes)
		bodyHash := hex.EncodeToString(bodyHashRaw[:])

		claims, _ := ClaimsFromContext(c)
		key := idempotencyCacheKey(claims.Subject, c.Request.Method, c.Request.URL.Path, rawKey)

		if raw, err := ns.Get(ctx, key); err == nil {
			var cached idempotentCached
			if json.Unmarshal([]byte(raw), &cached) == nil {
				if cached.BodyHash != bodyHash {
					c.AbortWithStatusJSON(http.StatusConflict,
						gin.H{"error": "idempotency key was already used with a different request body"})
					return
				}
				c.Data(cached.Status, "application/json; charset=utf-8", cached.Body)
				c.Abort()
				return
			}
		}

		lockKey := key + ":lock"
		acquired, lockErr := ns.SetNX(ctx, lockKey, "1", idempotencyLockTTL)
		if lockErr != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable,
				gin.H{"error": "could not acquire idempotency lock, please retry"})
			return
		}
		if !acquired {
			c.AbortWithStatusJSON(http.StatusConflict,
				gin.H{"error": "a request with this idempotency key is already in progress"})
			return
		}
		defer func() { _ = ns.Delete(ctx, lockKey) }()

		w := &idempotentWriter{ResponseWriter: c.Writer, status: http.StatusOK}
		c.Writer = w
		c.Next()

		if w.status >= 200 && w.status < 300 {
			if raw, err := json.Marshal(idempotentCached{
				Status: w.status, Body: w.body.Bytes(), BodyHash: bodyHash,
			}); err == nil {
				_ = ns.Set(ctx, key, raw, 24*time.Hour)
			}
		}
	}
}
