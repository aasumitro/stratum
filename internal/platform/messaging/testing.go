package messaging

import (
	"context"
	"time"
)

// NoopPublisher is a no-op EventPublisher for use in tests.
type NoopPublisher struct{}

func (NoopPublisher) Publish(_ context.Context, _, _ string, _ []byte) error { return nil }
func (NoopPublisher) PublishDelayed(_ context.Context, _, _ string, _ []byte, _ time.Duration) error {
	return nil
}
