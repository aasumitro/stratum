package connect

import "testing"

// Nothing listens on these ports: pools connect lazily, so these tests exercise the
// cache alone.
const (
	dsnA = "postgres://a@127.0.0.1:1/none?connect_timeout=1"
	dsnB = "postgres://b@127.0.0.1:1/none?connect_timeout=1"
)

func TestGet_ReusesThePoolForTheSameDSN(t *testing.T) {
	p := NewPostgresPool()
	t.Cleanup(p.CloseAll)

	first, err := p.Get("project", dsnA)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	second, err := p.Get("project", dsnA)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if first != second {
		t.Error("the same project and DSN should share one pool")
	}
}

func TestGet_ReplacesThePoolWhenTheDSNChanges(t *testing.T) {
	p := NewPostgresPool()
	t.Cleanup(p.CloseAll)

	old, err := p.Get("project", dsnA)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	replaced, err := p.Get("project", dsnB)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if replaced == old {
		t.Fatal("a changed DSN must not reuse the pool opened with the old one")
	}
	if user := replaced.Config().ConnConfig.User; user != "b" {
		t.Errorf("pool connects as %q, want b", user)
	}

	// The old pool is closed, so nothing can quietly keep using its credentials.
	if _, err := old.Acquire(t.Context()); err == nil {
		t.Error("the replaced pool should be closed")
	}

	// A call that read the old DSN just before the edit displaces the new pool, and
	// the next call with the current DSN puts it back.
	if stale, err := p.Get("project", dsnA); err != nil || stale.Config().ConnConfig.User != "a" {
		t.Fatalf("stale Get: pool %v, err %v", stale, err)
	}
	current, err := p.Get("project", dsnB)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if user := current.Config().ConnConfig.User; user != "b" {
		t.Errorf("pool connects as %q after the current DSN came back, want b", user)
	}
}

func TestGet_SetsTheApplicationName(t *testing.T) {
	p := NewPostgresPool()
	t.Cleanup(p.CloseAll)

	pool, err := p.Get("project", dsnA)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := pool.Config().ConnConfig.RuntimeParams["application_name"]; got != "stratum-studio" {
		t.Errorf("application_name = %q", got)
	}
}

func TestInvalidate_ClosesAndForgetsThePool(t *testing.T) {
	p := NewPostgresPool()
	t.Cleanup(p.CloseAll)

	pool, err := p.Get("project", dsnA)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	p.Invalidate("project")
	if _, err := pool.Acquire(t.Context()); err == nil {
		t.Error("the invalidated pool should be closed")
	}

	again, err := p.Get("project", dsnA)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if again == pool {
		t.Error("Get after Invalidate should open a new pool")
	}
	p.Invalidate("never-seen")
}
