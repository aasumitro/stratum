package messaging

import (
	"context"
	"fmt"
	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Handler processes one delivery's body. Returning an error causes the
// delivery to be Rejected (which — see topology.go's QueueSpec doc —
// increments delivery-count, triggering the queue's native delayed retry
// and eventually dead-lettering once MaxDeliveries is exhausted).
// Returning nil Acks the delivery.
//
// Handlers receive ctx carrying the request-scoped logger (via
// internal/platform/logger.WithContext) so processing code can log with
// the same correlation as the rest of the app.
type Handler func(ctx context.Context, body []byte) error

// ConsumerSpec bundles everything one Consumer needs to declare its
// topology and start consuming: the exchange it reads from, the queue
// it owns, the module's shared DLX, and how many deliveries to prefetch.
type ConsumerSpec struct {
	Exchange      ExchangeSpec
	Queue         QueueSpec
	DLX           DLXSpec
	PrefetchCount int // Channel.Qos prefetch — bounds in-flight deliveries per consumer goroutine
}

// Consumer owns one durable queue and one Handler. It re-declares its
// topology and resumes consuming after every connection-level reconnect
// (see Connection.NotifyReconnect), so a network blip doesn't leave it
// silently not consuming.
type Consumer struct {
	conn    *Connection
	spec    ConsumerSpec
	handler Handler
	logger  *slog.Logger
}

// NewConsumer constructs a Consumer. Call Run to start consuming —
// construction alone does not declare topology or consume anything,
// so callers can build all of a module's consumers before starting any
// of them (useful for clean startup ordering in main.go).
func NewConsumer(conn *Connection, spec ConsumerSpec, handler Handler, logger *slog.Logger) *Consumer {
	return &Consumer{conn: conn, spec: spec, handler: handler, logger: logger}
}

// Run declares topology and consumes until ctx is cancelled, transparently
// resuming after reconnects. Run blocks — call it in its own goroutine.
func (c *Consumer) Run(ctx context.Context) {
	for {
		if err := c.runOnce(ctx); err != nil {
			c.logger.Error("consumer stopped, will resume after reconnect",
				"queue", c.spec.Queue.Name, "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-c.conn.NotifyReconnect():
			// loop back into runOnce, which redeclares topology fresh
		}
	}
}

// runOnce declares this consumer's full topology (exchange, DLX, queue,
// bindings) on a fresh channel, then consumes deliveries until the
// channel/connection breaks or ctx is cancelled.
func (c *Consumer) runOnce(ctx context.Context) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("opening consumer channel: %w", err)
	}
	defer func() { _ = ch.Close() }()

	if err := DeclareExchange(ch, c.spec.Exchange); err != nil {
		return err
	}
	if err := DeclareDLX(ch, c.spec.DLX); err != nil {
		return err
	}
	if err := DeclareQueue(ch, c.spec.Exchange.Name, c.spec.Queue); err != nil {
		return err
	}

	prefetch := c.spec.PrefetchCount
	if prefetch <= 0 {
		prefetch = 10
	}
	if err := ch.Qos(prefetch, 0, false); err != nil {
		return fmt.Errorf("setting qos: %w", err)
	}

	deliveries, err := ch.ConsumeWithContext(
		ctx,
		c.spec.Queue.Name,
		"",    // consumer tag: let the server generate one
		false, // autoAck: false — we explicitly Ack/Reject per the Handler result
		false, // exclusive
		false, // noLocal (unused by RabbitMQ)
		false, // noWait
		nil,   // args
	)
	if err != nil {
		return fmt.Errorf("starting consume on %q: %w", c.spec.Queue.Name, err)
	}

	c.logger.Info("consumer started", "queue", c.spec.Queue.Name)

	for delivery := range deliveries {
		c.handleDelivery(ctx, delivery)
	}

	// deliveries channel closed: either ctx was cancelled (clean exit,
	// caller's ctx.Done() check in Run will return) or the channel/
	// connection broke (Run will wait for NotifyReconnect and retry).
	return nil
}

func (c *Consumer) handleDelivery(ctx context.Context, delivery amqp.Delivery) {
	// Continue the producer's trace (see publisher.go's publish) instead of
	// starting a disconnected one.
	carrier := propagation.MapCarrier{}
	for k, v := range delivery.Headers {
		if s, ok := v.(string); ok {
			carrier[k] = s
		}
	}
	msgCtx := otel.GetTextMapPropagator().Extract(ctx, carrier)
	msgCtx, span := otel.Tracer("stratum/messaging").Start(msgCtx, "amqp.consume "+c.spec.Queue.Name,
		trace.WithSpanKind(trace.SpanKindConsumer))
	defer span.End()

	err := c.handler(msgCtx, delivery.Body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		c.logger.Warn("delivery processing failed, rejecting for retry",
			"queue", c.spec.Queue.Name, "error", err)

		// Reject (not Nack) so delivery-count increments — required for
		// the queue's x-delayed-retry-type:"failed" and x-delivery-limit
		// to engage. See topology.go's QueueSpec doc for the full
		// reasoning and the RabbitMQ docs distinction between the two.
		if rejectErr := delivery.Reject(true); rejectErr != nil {
			c.logger.Error("failed to reject delivery", "error", rejectErr)
		}
		return
	}

	if ackErr := delivery.Ack(false); ackErr != nil {
		c.logger.Error("failed to ack delivery", "error", ackErr)
	}
}
