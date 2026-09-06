package messaging

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// TestPublisherWatchersStopOnContextCancel proves the channel-reopen retry
// loop honours the shutdown context: once it is cancelled the watcher
// goroutine returns promptly instead of sleeping out the (capped 30s)
// backoff, so Publisher.Close's wg.Wait cannot hang the process on exit.
func TestPublisherWatchersStopOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	p := &Publisher{
		// A Connection with no live amqp.Connection: openChannel returns an
		// error rather than panicking, which forces reopenWithBackoff onto
		// its retry-with-backoff path — the branch under test.
		conn:   &Connection{},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		ctx:    ctx,
	}

	// Stand in for openChannel's spawn: a watcher tracked by the WaitGroup
	// that is already past its close-notify and down in the retry loop
	// against a broker that will not come back.
	dead := &amqp.Channel{}
	p.ch = dead
	p.wg.Go(func() { p.reopenWithBackoff(dead) })

	drained := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(drained)
	}()

	select {
	case <-drained:
		t.Fatal("watcher exited before the context was cancelled")
	case <-time.After(100 * time.Millisecond):
	}

	cancel()

	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not return after context cancellation; Close would block on wg.Wait")
	}

	// Close has nothing left to wait on now, and no channel to close.
	p.ch = nil
	if err := p.Close(); err != nil {
		t.Fatalf("Close after cancellation: %v", err)
	}
}
