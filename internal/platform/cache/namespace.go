package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Namespace wraps a shared *redis.Client with a fixed key prefix, so each
// module's cache calls (see modules/billing/module.go's New(...), etc.)
// can only ever read or write its own keys. This is what lets multiple
// modules share one Redis instance without a naming collision turning
// into a cross-module coupling bug — the same logical isolation we get
// from Postgres schemas, applied to Redis.
//
// Namespace deliberately exposes a small surface (Get/Set/Delete/Exists)
// rather than the full *redis.Client, so module code can't reach for
// cluster-wide commands (FLUSHALL, KEYS *, etc.) that would defeat the
// isolation this type exists to provide. A module that genuinely needs a
// Redis feature not exposed here should get it added intentionally,
// rather than reaching past Namespace at the call site.
type Namespace struct {
	client *redis.Client
	prefix string
}

// NewNamespace returns a Namespace scoped to prefix (e.g. "billing",
// "notification"). Every key passed to Get/Set/Delete/Exists is prefixed
// with "prefix:" before reaching Redis.
func NewNamespace(client *redis.Client, prefix string) *Namespace {
	return &Namespace{client: client, prefix: prefix}
}

func (n *Namespace) key(k string) string {
	return fmt.Sprintf("%s:%s", n.prefix, k)
}

// Get returns the string value at key, or redis.Nil if it doesn't exist
// — callers should check errors.Is(err, redis.Nil) the same way they
// would with a raw *redis.Client, since Namespace intentionally doesn't
// hide that sentinel behind a different error type.
func (n *Namespace) Get(ctx context.Context, key string) (string, error) {
	return n.client.Get(ctx, n.key(key)).Result()
}

// Set stores value at key with optional ttl (0 means no expiry).
func (n *Namespace) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	return n.client.Set(ctx, n.key(key), value, ttl).Err()
}

// SetNX atomically sets key to value only if it doesn't already exist,
// returning whether the set happened. Used for short-lived locks (e.g. an
// idempotency in-flight guard) where two concurrent callers must not both
// proceed.
func (n *Namespace) SetNX(ctx context.Context, key string, value any, ttl time.Duration) (bool, error) {
	return n.client.SetNX(ctx, n.key(key), value, ttl).Result()
}

// Delete removes key. Deleting a non-existent key is not an error.
func (n *Namespace) Delete(ctx context.Context, key string) error {
	return n.client.Del(ctx, n.key(key)).Err()
}

// Exists reports whether key is currently set.
func (n *Namespace) Exists(ctx context.Context, key string) (bool, error) {
	count, err := n.client.Exists(ctx, n.key(key)).Result()
	if err != nil {
		return false, fmt.Errorf("cache.Exists: %w", err)
	}
	return count > 0, nil
}

// Incr atomically increments the integer value at key by 1, creating it
// at 0 first if absent, and returns the new value. Used by sessions/
// rate-limiting code that needs an atomic counter rather than Get+Set.
func (n *Namespace) Incr(ctx context.Context, key string) (int64, error) {
	return n.client.Incr(ctx, n.key(key)).Result()
}

// Expire sets or refreshes a key's TTL without changing its value.
func (n *Namespace) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return n.client.Expire(ctx, n.key(key), ttl).Err()
}
