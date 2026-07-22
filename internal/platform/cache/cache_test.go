package cache_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/cache"
	"github.com/aasumitro/stratum/internal/platform/config"
)

func testClient(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	c, err := cache.NewClient(t.Context(), config.RedisConfig{URL: url})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestNewClient_InvalidURL(t *testing.T) {
	if _, err := cache.NewClient(t.Context(), config.RedisConfig{URL: "not-a-redis-url"}); err == nil {
		t.Error("expected an error for an invalid redis URL")
	}
}

func TestNamespace_SetGetDeleteExists(t *testing.T) {
	ns := cache.NewNamespace(testClient(t), "test-ns-basic")
	ctx := t.Context()

	// miss → redis.Nil
	if _, err := ns.Get(ctx, "missing"); !errors.Is(err, redis.Nil) {
		t.Errorf("Get miss: want redis.Nil, got %v", err)
	}

	if err := ns.Set(ctx, "k1", "v1", time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, err := ns.Get(ctx, "k1"); err != nil || got != "v1" {
		t.Errorf("Get k1 = (%q, %v), want (v1, nil)", got, err)
	}
	if ok, _ := ns.Exists(ctx, "k1"); !ok {
		t.Error("Exists(k1) = false, want true")
	}
	if err := ns.Delete(ctx, "k1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if ok, _ := ns.Exists(ctx, "k1"); ok {
		t.Error("Exists(k1) after delete = true, want false")
	}
}

func TestNamespace_SetNX_Atomicity(t *testing.T) {
	ns := cache.NewNamespace(testClient(t), "test-ns-setnx")
	ctx := t.Context()
	_ = ns.Delete(ctx, "lock")

	first, err := ns.SetNX(ctx, "lock", "1", time.Minute)
	if err != nil || !first {
		t.Fatalf("first SetNX = (%v, %v), want (true, nil)", first, err)
	}
	second, err := ns.SetNX(ctx, "lock", "1", time.Minute)
	if err != nil || second {
		t.Errorf("second SetNX = (%v, %v), want (false, nil) — key already held", second, err)
	}
}

func TestNamespace_IncrAndExpire(t *testing.T) {
	ns := cache.NewNamespace(testClient(t), "test-ns-incr")
	ctx := t.Context()
	_ = ns.Delete(ctx, "counter")

	if n, err := ns.Incr(ctx, "counter"); err != nil || n != 1 {
		t.Fatalf("first Incr = (%d, %v), want (1, nil)", n, err)
	}
	if n, _ := ns.Incr(ctx, "counter"); n != 2 {
		t.Errorf("second Incr = %d, want 2", n)
	}
	if err := ns.Expire(ctx, "counter", time.Hour); err != nil {
		t.Errorf("Expire: %v", err)
	}
}

func TestNamespace_PrefixIsolation(t *testing.T) {
	client := testClient(t)
	ctx := t.Context()
	a := cache.NewNamespace(client, "modA")
	b := cache.NewNamespace(client, "modB")

	if err := a.Set(ctx, "shared", "fromA", time.Minute); err != nil {
		t.Fatal(err)
	}
	// same logical key, different namespace → must not see A's value
	if _, err := b.Get(ctx, "shared"); !errors.Is(err, redis.Nil) {
		t.Error("namespaces must not collide on the same key")
	}
}

func TestRateLimiter_AllowsThenBlocks(t *testing.T) {
	rl := cache.NewRateLimiter(testClient(t), "test-rl")
	ctx := t.Context()
	limit := cache.PerHour(2) // 2 per hour, burst 2

	key := "user-" + time.Now().Format("150405.000000")
	r1, err := rl.Allow(ctx, key, limit)
	if err != nil || !r1.Allowed {
		t.Fatalf("first request should be allowed: %+v, %v", r1, err)
	}
	r2, _ := rl.Allow(ctx, key, limit)
	if !r2.Allowed {
		t.Errorf("second request should be allowed (burst=2): %+v", r2)
	}
	r3, _ := rl.Allow(ctx, key, limit)
	if r3.Allowed {
		t.Errorf("third request should be blocked: %+v", r3)
	}
	if r3.RetryAfter <= 0 {
		t.Errorf("blocked result should carry a positive RetryAfter, got %v", r3.RetryAfter)
	}
}

func TestRateLimit_Builders(t *testing.T) {
	if cache.PerSecond(5).Rate != 5 || cache.PerMinute(5).Period != time.Minute || cache.PerHour(5).Period != time.Hour {
		t.Error("rate builders produced unexpected Rate/Period")
	}
}

// --- CachedOrganizationReader ---

type countingOrgReader struct {
	contracts.OrganizationReader
	orgCalls  int
	roleCalls int
	name      string
	role      string
}

func (r *countingOrgReader) GetOrganizationByID(_ context.Context, id string) (*contracts.OrganizationInfo, error) {
	r.orgCalls++
	return &contracts.OrganizationInfo{ID: id, Name: r.name}, nil
}

func (r *countingOrgReader) GetMemberRole(_ context.Context, _, _ string) (string, error) {
	r.roleCalls++
	return r.role, nil
}

func TestCachedOrganizationReader_CachesAndInvalidates(t *testing.T) {
	client := testClient(t)
	inner := &countingOrgReader{name: "Acme", role: "admin"}
	c := cache.NewCachedOrganizationReader(inner, client)
	ctx := t.Context()
	orgID := "org-cache-" + time.Now().Format("150405.000000")

	// clear any stale keys from a prior run
	c.InvalidateOrganization(ctx, orgID)

	// first read hits the inner reader; second is served from cache
	if _, err := c.GetOrganizationByID(ctx, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetOrganizationByID(ctx, orgID); err != nil {
		t.Fatal(err)
	}
	if inner.orgCalls != 1 {
		t.Errorf("GetOrganizationByID inner calls = %d, want 1 (second served from cache)", inner.orgCalls)
	}

	// after invalidation the next read hits the inner reader again
	c.InvalidateOrganization(ctx, orgID)
	if _, err := c.GetOrganizationByID(ctx, orgID); err != nil {
		t.Fatal(err)
	}
	if inner.orgCalls != 2 {
		t.Errorf("after invalidation inner calls = %d, want 2", inner.orgCalls)
	}
}

func TestCachedOrganizationReader_MemberRoleCacheAndIsMember(t *testing.T) {
	client := testClient(t)
	inner := &countingOrgReader{role: "member"}
	c := cache.NewCachedOrganizationReader(inner, client)
	ctx := t.Context()
	orgID := "org-role-" + time.Now().Format("150405.000000")
	sub := "sub-1"
	c.InvalidateMemberRole(ctx, orgID, sub)

	role, err := c.GetMemberRole(ctx, orgID, sub)
	if err != nil || role != "member" {
		t.Fatalf("GetMemberRole = (%q, %v)", role, err)
	}
	// second call cached
	_, _ = c.GetMemberRole(ctx, orgID, sub)
	if inner.roleCalls != 1 {
		t.Errorf("role inner calls = %d, want 1", inner.roleCalls)
	}

	// IsMember is derived from the (now cached) role
	ok, _ := c.IsMember(ctx, orgID, sub)
	if !ok {
		t.Error("IsMember should be true for a non-empty role")
	}

	c.InvalidateMemberRole(ctx, orgID, sub)
	_, _ = c.GetMemberRole(ctx, orgID, sub)
	if inner.roleCalls != 2 {
		t.Errorf("after role invalidation inner calls = %d, want 2", inner.roleCalls)
	}
}
