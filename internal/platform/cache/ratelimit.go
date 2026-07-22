package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis_rate/v10"
	"github.com/redis/go-redis/v9"
)

// RateLimiter wraps go-redis/redis_rate (GCRA / leaky-bucket algorithm —
// chosen over a hand-rolled Lua script because it's the official,
// battle-tested companion package to go-redis itself, and reinventing
// GCRA's Lua script is an easy place to introduce a subtle race
// condition). Like Namespace, every key is prefixed per-module so two
// modules rate-limiting on, say, "user:123" can't collide.
type RateLimiter struct {
	limiter *redis_rate.Limiter
	prefix  string
}

// NewRateLimiter returns a RateLimiter scoped to prefix (e.g.
// "billing.webhook", "notification.send"). client is the same shared
// *redis.Client used elsewhere — redis_rate uses its own Lua script
// against whatever keys we pass it, so it composes fine with Namespace
// using the same underlying client without any coordination needed.
func NewRateLimiter(client *redis.Client, prefix string) *RateLimiter {
	return &RateLimiter{limiter: redis_rate.NewLimiter(client), prefix: prefix}
}

func (r *RateLimiter) key(k string) string {
	return fmt.Sprintf("%s:%s", r.prefix, k)
}

// Limit describes a rate: Rate requests allowed per Period, with Burst
// extra requests permitted on top of the steady rate (GCRA's notion of
// burst capacity). Use the PerSecond/PerMinute/PerHour helpers below for
// the common case of Burst == Rate.
type Limit = redis_rate.Limit

// PerSecond - PerMinute - PerHour build a Limit allowing rate requests per
// the named period, with burst capacity equal to rate.
func PerSecond(rate int) Limit { return redis_rate.PerSecond(rate) }
func PerMinute(rate int) Limit { return redis_rate.PerMinute(rate) }
func PerHour(rate int) Limit   { return redis_rate.PerHour(rate) }

// Result reports the outcome of a rate-limit check.
type Result struct {
	Allowed    bool          // whether this request may proceed
	Remaining  int           // requests remaining in the current window
	RetryAfter time.Duration // if not Allowed, how long until the next request may succeed
	ResetAfter time.Duration // time until the window resets to full capacity
}

// Allow checks whether one request against key is permitted under limit,
// consuming one unit of quota if so. key should already identify the
// thing being limited (e.g. a user ID, IP, or API key) — the module
// prefix is added on top, so callers don't need to namespace it themselves.
func (r *RateLimiter) Allow(ctx context.Context, key string, limit Limit) (*Result, error) {
	res, err := r.limiter.Allow(ctx, r.key(key), limit)
	if err != nil {
		return nil, fmt.Errorf("checking rate limit for %q: %w", key, err)
	}
	return &Result{
		Allowed:    res.Allowed > 0,
		Remaining:  res.Remaining,
		RetryAfter: res.RetryAfter,
		ResetAfter: res.ResetAfter,
	}, nil
}
