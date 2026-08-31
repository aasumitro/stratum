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

	// ctx is the process shutdown context. Every watcher goroutine selects
	// on ctx.Done() so a shutdown stops them instead of leaving one retrying
	// a dead broker forever.
	ctx context.Context
	// wg tracks the watcher goroutines (watchReconnect, and one
	// watchChannelClose per opened channel) so Close can wait for them to
	// unwind before the process exits.
	wg sync.WaitGroup

	mu sync.RWMutex
	ch *amqp.Channel
}

// NewPublisher opens a confirm-mode channel on conn and starts a
// background goroutine that re-opens the channel after every reconnect.
// ctx is the process shutdown context; the background watchers stop when it
// is cancelled.
func NewPublisher(ctx context.Context, conn *Connection, logger *slog.Logger) (*Publisher, error) {
	p := &Publisher{conn: conn, logger: logger, ctx: ctx}

	if err := p.openChannel(); err != nil {
		return nil, fmt.Errorf("messaging.NewPublisher: %w", err)
	}

	p.wg.Go(p.watchReconnect)

	return p, nil
}

func (p *Publisher) openChannel() error {
	ch, err := p.conn.Channel()
	if err != nil {
		return fmt.Errorf("messaging.openChannel: %w", err)
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

	p.swapChannel(ch)

	// wg.Go here is reached from watchReconnect and reopenWithBackoff, which
	// are themselves wg-tracked. That is safe only because watchReconnect is
	// started once in NewPublisher and returns solely on ctx.Done(): it holds
	// the WaitGroup counter >= 1 for the whole process lifetime, so this Add
	// can never race Close's wg.Wait from a zero counter.
	p.wg.Go(func() { p.watchChannelClose(ch) })

	return nil
}

// swapChannel installs newCh as the publisher's current channel and closes whatever channel it
// replaces. Closing the old channel is what makes reopenWithBackoff and watchReconnect safe to
// race each other: whichever one loses gets its channel closed here by the winner, which both
// releases the broker-side channel and — because Close() triggers a graceful close, which
// watchChannelClose already treats as "don't reopen" — lets the loser's watchChannelClose
// goroutine observe that close and exit, instead of leaking a channel and a parked goroutine.
func (p *Publisher) swapChannel(newCh *amqp.Channel) {
	p.mu.Lock()
	old := p.ch
	p.ch = newCh
	p.mu.Unlock()

	if old != nil && old != newCh {
		_ = old.Close()
	}
}

func (p *Publisher) watchReconnect() {
	for {
		reconnected := p.conn.NotifyReconnect()
		select {
		case <-p.ctx.Done():
			return
		case <-reconnected:
		}

		if err := p.openChannel(); err != nil {
			p.logger.Error("publisher failed to reopen channel after reconnect", "error", err)
			// Will retry on the *next* reconnect signal — acceptable
			// because Connection itself keeps retrying the underlying
			// TCP connection independently of this loop.
		}
	}
}

// watchChannelClose watches ch for a broker-initiated close and reopens it with backoff.
// This is a channel-level failure (the connection stays up but the broker closes this one
// channel, e.g. on a protocol violation) — distinct from watchReconnect's connection-level
// failure. A graceful close (Close() called intentionally, so the notify channel closes
// with no error) does not trigger a reopen.
func (p *Publisher) watchChannelClose(ch *amqp.Channel) {
	notify := ch.NotifyClose(make(chan *amqp.Error, 1))
	select {
	case <-p.ctx.Done():
		return
	case err, ok := <-notify:
		if !ok || err == nil {
			return
		}
	}
	p.reopenWithBackoff(ch)
}

// reopenWithBackoff replaces dead with a freshly opened channel, retrying with capped
// exponential backoff until it succeeds or dead is no longer the publisher's current
// channel — meaning watchReconnect, or an earlier winning iteration of this same loop,
// already replaced it, in which case this loop stands down rather than fighting it. The
// identity check runs before every attempt (including the first), not once at entry, which
// is what lets it stand down cleanly mid-retry instead of two loops racing to install a
// channel. Retry is unbounded: this loop retries a single shared connection-level resource
// with no per-attempt accumulation cost, unlike the outbox's persisted-row backoff, so
// there is no equivalent of maxAttempts to cap it against — a broker that stays down keeps
// this loop backing off (capped at maxBackoff) rather than giving up permanently.
func (p *Publisher) reopenWithBackoff(dead *amqp.Channel) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		if p.ctx.Err() != nil {
			return
		}

		p.mu.RLock()
		current := p.ch
		p.mu.RUnlock()
		if current != dead {
			return
		}

		err := p.openChannel()
		if err == nil {
			// openChannel already assigned p.ch and spawned that channel's own
			// watchChannelClose — nothing left to do.
			return
		}
		p.logger.Warn("failed to reopen publisher channel after channel-level close, retrying", "error", err, "backoff", backoff)

		select {
		case <-time.After(backoff):
		case <-p.ctx.Done():
			return
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
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

// Close closes the publisher's channel and waits for the watcher goroutines
// to return. The underlying Connection is owned by main.go and closed
// separately.
func (p *Publisher) Close() error {
	p.mu.RLock()
	ch := p.ch
	p.mu.RUnlock()

	var err error
	if ch != nil {
		err = ch.Close()
	}

	// wg.Wait cannot deadlock here: both RunAPI and RunWorker only reach this
	// call — through the deferred Infra.Close → mqShutdown chain — after their
	// signal context is already Done, so every watcher has already observed
	// ctx.Done() and is on its way out. Each watcher's select reaches that
	// case with no blocking work in front of it.
	p.wg.Wait()
	return err
}
