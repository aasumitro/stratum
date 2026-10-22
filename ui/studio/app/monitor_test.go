package app

import (
	"testing"
	"time"
)

// TestStartPoller_RecoversPanicAndCleansUp drives the poller goroutine into a
// panic and asserts it is contained: the process stays up and the poller's
// map entry is removed, exactly as it would be for a cancelled poller.
//
// The panic seam is a MonitorService whose ProjectService has a nil *sql.DB:
// the goroutine's first pollCheck calls projects.Get, which dereferences the
// nil DB and panics — a panic raised inside pollCheck, which is what the
// deferred recover in StartPoller has to catch.
func TestStartPoller_RecoversPanicAndCleansUp(t *testing.T) {
	s := NewMonitorService(nil, NewProjectService(nil))

	if err := s.StartPoller("proj-panic", 60); err != nil {
		t.Fatalf("StartPoller: %v", err)
	}

	// The goroutine panics on its first pollCheck; the deferred recover
	// swallows it and the deferred cleanup drops the map entry. Poll
	// IsPolling until that happens rather than sleeping a fixed time.
	deadline := time.Now().Add(5 * time.Second)
	for s.IsPolling("proj-panic") {
		if time.Now().After(deadline) {
			t.Fatal("poller still registered after panic: recover or map cleanup did not run")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
