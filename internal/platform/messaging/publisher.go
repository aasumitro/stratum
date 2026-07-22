package messaging

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

const confirmTimeout = 5 * time.Second

var _ EventPublisher = (*Publisher)(nil)

// EventPublisher is the interface modules depend on to publish domain events.
// The concrete implementation (Publisher below, backed by amqp091-go)
// lives in this package; modules receive it through their constructor
// (see module.go's New(...) pattern) rather than importing amqp091-go
// directly. This is what keeps a module's publish call sites unchanged
// if the underlying broker client ever changes.
type EventPublisher interface {
	// Publish sends body to exchange with routingKey. body is expected to
	// already be the serialized contracts/events.Envelope — this
	// interface does not know about Envelope to avoid a dependency from
	// platform/messaging back up to contracts (platform must not depend
	// on contracts; contracts and modules depend on platform).
	Publish(ctx context.Context, exchange, routingKey string, body []byte) error

	// PublishDelayed sends body to exchange with a per-message TTL.
	// Used with a parking queue (no consumer) + DLX to achieve delayed delivery.
	PublishDelayed(ctx context.Context, exchange, routingKey string, body []byte, delay time.Duration) error
}

// Publisher is the concrete EventPublisher backed by amqp091-go, with
// publisher confirms enabled so Publish does not return successfully
// until the broker has actually accepted the message — without this, a
// network blip between us and the broker could silently drop an event
// with no error surfaced anywhere.
//
// One Publisher is shared process-wide (constructed once in main.go,
// passed into every module) since AMQP channels handle concurrent
// publishes safely as long as callers don't also Ack/Nack deliveries on
// the same channel — and Publisher never does, consumers get their own
// channel (see consumer.go).
type Publisher struct {
	conn   *Connection
	logger *slog.Logger

	mu sync.RWMutex
	ch *amqp.Channel
}

// NewPublisher opens a confirm-mode channel on conn and starts a
// background goroutine that re-opens the channel after every reconnect.
func NewPublisher(conn *Connection, logger *slog.Logger) (*Publisher, error) {
	p := &Publisher{conn: conn, logger: logger}

	if err := p.openChannel(); err != nil {
		return nil, err
	}

	go p.watchReconnect()

	return p, nil
}

func (p *Publisher) openChannel() error {
	ch, err := p.conn.Channel()
	if err != nil {
		return fmt.Errorf("opening publisher channel: %w", err)
	}
	if err := ch.Confirm(false); err != nil {
		return fmt.Errorf("enabling confirm mode: %w", err)
	}

	// Required because Publish sets mandatory:true. Without a listener,
	// an unroutable message (no queue bound to that routing key) is
	// still confirmed as if delivered — see Channel.Confirm's own docs:
	// "Unroutable mandatory ... messages are acknowledged immediately
	// after any Channel.NotifyReturn listeners have been notified."
	// This goroutine is what turns a misconfigured routing key into a
	// visible warning instead of a silently swallowed event.
	returns := ch.NotifyReturn(make(chan amqp.Return, 16))
	go func() {
		for ret := range returns {
			p.logger.Warn("publish returned as unroutable",
				"exchange", ret.Exchange, "routing_key", ret.RoutingKey, "reply_text", ret.ReplyText)
		}
	}()

	p.mu.Lock()
	p.ch = ch
	p.mu.Unlock()

	return nil
}

func (p *Publisher) watchReconnect() {
	for {
		reconnected := p.conn.NotifyReconnect()
		<-reconnected

		if err := p.openChannel(); err != nil {
			p.logger.Error("publisher failed to reopen channel after reconnect", "error", err)
			// Will retry on the *next* reconnect signal — acceptable
			// because Connection itself keeps retrying the underlying
			// TCP connection independently of this loop.
		}
	}
}

// publish is the shared send-and-confirm implementation. mandatory=true for
// normal publishes (broker must route it); false for delayed/parking queues.
func (p *Publisher) publish(ctx context.Context, exchange, routingKey string, mandatory bool, msg amqp.Publishing) error {
	// Carry the producer's trace context in message headers so the consumer
	// (see consumer.go's handleDelivery) can continue the same trace instead
	// of starting a disconnected one.
	if msg.Headers == nil {
		msg.Headers = amqp.Table{}
	}
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	for k, v := range carrier {
		msg.Headers[k] = v
	}

	p.mu.RLock()
	ch := p.ch
	p.mu.RUnlock()

	if ch == nil {
		return fmt.Errorf("publisher channel not ready")
	}

	confirmation, err := ch.PublishWithDeferredConfirmWithContext(ctx, exchange, routingKey, mandatory, false, msg)
	if err != nil {
		return fmt.Errorf("publishing to %s/%s: %w", exchange, routingKey, err)
	}
	if confirmation == nil {
		// Channel wasn't in confirm mode — should be unreachable since
		// openChannel always calls Confirm(false), but guard anyway
		// rather than letting Wait() panic on a nil receiver.
		return fmt.Errorf("publish to %s/%s returned no confirmation (channel not in confirm mode)", exchange, routingKey)
	}

	waitCtx, cancel := context.WithTimeout(ctx, confirmTimeout)
	defer cancel()

	ok, err := confirmation.WaitContext(waitCtx)
	if err != nil {
		return fmt.Errorf("waiting for broker confirmation on %s/%s: %w", exchange, routingKey, err)
	}
	if !ok {
		return fmt.Errorf("broker nacked publish to %s/%s", exchange, routingKey)
	}
	return nil
}

// Publish implements EventPublisher. It blocks until the broker confirms receipt or confirmTimeout elapses.
func (p *Publisher) Publish(ctx context.Context, exchange, routingKey string, body []byte) error {
	return p.publish(ctx, exchange, routingKey, true, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
		Body:         body,
	})
}

// PublishDelayed implements EventPublisher. Sets a per-message TTL via the Expiration header.
// The target exchange should route to a parking queue whose DLX delivers to the real consumer queue once the TTL expires.
func (p *Publisher) PublishDelayed(ctx context.Context, exchange, routingKey string, body []byte, delay time.Duration) error {
	ms := max(delay.Milliseconds(), 1)
	return p.publish(ctx, exchange, routingKey, false, amqp.Publishing{ // mandatory: false — parking queue may not exist yet on first deploy
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
		Expiration:   fmt.Sprintf("%d", ms),
		Body:         body,
	})
}

// Close closes the publisher's channel. The underlying Connection is
// owned by main.go and closed separately.
func (p *Publisher) Close() error {
	p.mu.RLock()
	ch := p.ch
	p.mu.RUnlock()

	if ch != nil {
		return ch.Close()
	}
	return nil
}
