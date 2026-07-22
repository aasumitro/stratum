package notification_test

// Integration tests for the SSE stream's per-subject concurrency cap — need
// a real Redis, since the cap is enforced via Redis INCR/DECR on a
// "sse:open:<subject>" key.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/aasumitro/stratum/internal/modules/notification"
	"github.com/aasumitro/stratum/internal/platform/cache"
	"github.com/aasumitro/stratum/internal/platform/config"
)

func testRedisNotif(t *testing.T) *goredis.Client {
	t.Helper()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	c, err := cache.NewClient(t.Context(), config.RedisConfig{URL: url})
	if err != nil {
		t.Fatalf("redis client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// closeNotifyRecorder adds http.CloseNotifier to httptest.ResponseRecorder
// — gin.Context.Stream requires it on the underlying ResponseWriter and
// panics via a failed type assertion otherwise. The channel is never
// signaled; these tests end the stream via request-context cancellation
// instead, which the handler's select loop already handles.
type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
	closed chan bool
}

func newCloseNotifyRecorder() *closeNotifyRecorder {
	return &closeNotifyRecorder{ResponseRecorder: httptest.NewRecorder(), closed: make(chan bool, 1)}
}

func (c *closeNotifyRecorder) CloseNotify() <-chan bool {
	return c.closed
}

// waitForCounter polls key up to 2s for it to equal want — the stream's
// Incr/Decr happens on a separate goroutine in these tests, so the exact
// moment it lands isn't deterministic from the caller's side.
func waitForCounter(t *testing.T, redis *goredis.Client, key string, want int) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n, _ := redis.Get(t.Context(), key).Int(); n == want {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestIntegration_StreamNotifications_RejectsAtCap regression-tests that a
// caller already at the concurrent-stream cap is rejected with 429 rather
// than being allowed to accumulate an unbounded number of open streams, and
// that the rejected attempt doesn't itself leak an increment.
func TestIntegration_StreamNotifications_RejectsAtCap(t *testing.T) {
	pool := testPoolNotif(t)
	redis := testRedisNotif(t)

	const subject = "integ_sse_cap_user"
	key := "sse:open:" + subject
	t.Cleanup(func() { redis.Del(context.Background(), key) })

	redis.Set(t.Context(), key, 3, 0) // pre-fill to the cap

	e := notification.NewModuleEngineWithRedis(pool, subject, redis)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/me/notifications/stream", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("at cap: want 429, got %d: %s", w.Code, w.Body)
	}

	n, err := redis.Get(t.Context(), key).Int()
	if err != nil {
		t.Fatalf("get counter: %v", err)
	}
	if n != 3 {
		t.Errorf("counter after a rejected attempt = %d, want unchanged at 3", n)
	}
}

// TestIntegration_StreamNotifications_DecrementsOnClose regression-tests
// that opening a stream increments the per-subject counter and closing it
// (client disconnect / request context cancellation) decrements it back —
// without this, every stream ever opened would count against the cap
// forever.
func TestIntegration_StreamNotifications_DecrementsOnClose(t *testing.T) {
	pool := testPoolNotif(t)
	redis := testRedisNotif(t)

	const subject = "integ_sse_decr_user"
	key := "sse:open:" + subject
	t.Cleanup(func() { redis.Del(context.Background(), key) })

	e := notification.NewModuleEngineWithRedis(pool, subject, redis)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/me/notifications/stream", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		e.ServeHTTP(newCloseNotifyRecorder(), req)
		close(done)
	}()

	if !waitForCounter(t, redis, key, 1) {
		t.Fatal("counter never reached 1 after opening the stream")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after its context was cancelled")
	}

	if !waitForCounter(t, redis, key, 0) {
		t.Fatal("counter never returned to 0 after the stream closed")
	}
}
