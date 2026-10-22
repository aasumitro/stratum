package messaging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Setup dials RabbitMQ and constructs the process-wide Publisher. Modules
// receive *Publisher (via the EventPublisher interface) and *Connection
// (only consumers need this, to build their own Consumer instances)
// through their constructors — see each module's module.go.
//
// Mirrors otel.Setup's shape (returns the thing plus a shutdown func) for
// consistency across platform/ packages, even though messaging has no
// "disabled" state the way OTel does — every environment needs RabbitMQ
// since events are core to cross-module communication, not optional
// observability.
func Setup(ctx context.Context, url string, logger *slog.Logger) (*Connection, *Publisher, func() error, error) {
	conn, err := Dial(ctx, url, logger)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connecting to rabbitmq: %w", err)
	}

	publisher, err := NewPublisher(ctx, conn, logger)
	if err != nil {
		_ = conn.Close()
		return nil, nil, nil, fmt.Errorf("creating publisher: %w", err)
	}

	shutdown := func() error {
		return errors.Join(publisher.Close(), conn.Close())
	}

	return conn, publisher, shutdown, nil
}
