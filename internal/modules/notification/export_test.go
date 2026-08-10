package notification

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
)

// SetSSESlotDurationsForTest overrides the SSE concurrency-slot TTL and its
// refresh interval so a test can observe a refresh without waiting out the
// real multi-minute cadence. Mutates package-level state; call at the start
// of a test and restore via the returned func.
func SetSSESlotDurationsForTest(ttl, refresh time.Duration) (restore func()) {
	prevTTL, prevRefresh := sseSlotTTL, sseSlotRefreshInterval
	sseSlotTTL, sseSlotRefreshInterval = ttl, refresh
	return func() { sseSlotTTL, sseSlotRefreshInterval = prevTTL, prevRefresh }
}

// NewHandlerEngine returns a gin.Engine with notification handlers and no
// service. Only use for binding/validation tests.
func NewHandlerEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: "sub_owner"})
		c.Next()
	})
	h := &handler{svc: nil}
	e.GET("/notifications", h.listNotifications)
	e.GET("/notifications/unread-count", h.unreadCount)
	e.PATCH("/notifications/:id/read", h.markRead)
	return e
}

// NewModuleForTest creates a Module backed by a real pool.
func NewModuleForTest(pool *pgxpool.Pool) *Module {
	return New(pool, nil, nil, nil, "", nil)
}

// NewModuleEngine creates a full gin.Engine backed by a real DB module.
func NewModuleEngine(pool *pgxpool.Pool, authSub string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	mod := New(pool, nil, nil, nil, "", nil)
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub})
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	mod.Register(api, httpserver.RouteDeps{Auth: authMW, RateLimit: noopMW})
	return e
}

// NewModuleEngineWithRedis is NewModuleEngine but wires a real Redis client
// — needed to reach GET /me/notifications/stream at all (Register only
// mounts it when svc.redis is non-nil) and to test its per-subject
// concurrent-stream cap, which is enforced via Redis INCR/DECR.
func NewModuleEngineWithRedis(pool *pgxpool.Pool, authSub string, redis *goredis.Client) *gin.Engine {
	gin.SetMode(gin.TestMode)
	mod := New(pool, nil, nil, nil, "", redis)
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub})
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	mod.Register(api, httpserver.RouteDeps{Auth: authMW, AuthSSE: authMW, RateLimit: noopMW})
	return e
}
