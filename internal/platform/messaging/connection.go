// Package messaging wraps github.com/rabbitmq/amqp091-go with the
// reconnection logic, topology helpers, and publisher/consumer
// abstractions every module needs. amqp091-go deliberately does not
// auto-reconnect (the maintainers' own rationale: topology re-declaration
// after a reconnect is application-specific) — Connection below is our
// caller-side implementation of that reconnect loop, done once here so no
// module reimplements it.
package messaging

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	reconnectDelay   = 3 * time.Second
	reinitDelay      = 2 * time.Second
	resendDelay      = 5 * time.Second
	heartbeatTimeout = 10 * time.Second
)

// Connection wraps a single AMQP connection plus the channel publishers
// use, transparently reconnecting both on failure. Each module gets its
// own Channel (via NewChannel) for consuming, since AMQP channels aren't
// safe for concurrent use by multiple goroutines doing unrelated work,
// but all modules share one underlying TCP Connection — opening a new
// TCP connection per module would waste a file descriptor and a TLS
// handshake for no isolation benefit, since channels already provide that.
type Connection struct {
	url    string
	logger *slog.Logger

	mu        sync.RWMutex
	conn      *amqp.Connection
	closed    bool
	closeCh   chan struct{}
	notifyMu  sync.Mutex
	notifiers []chan struct{} // closed and recreated on every successful reconnect, so consumers waiting on it wake up and re-declare their topology
}

// Dial connects to url and starts the background reconnect loop. Call
// Close when the application shuts down to stop the loop and close the
// underlying connection.
func Dial(ctx context.Context, url string, logger *slog.Logger) (*Connection, error) {
	c := &Connection{
		url:     url,
		logger:  logger,
		closeCh: make(chan struct{}),
	}

	if err := c.connect(); err != nil {
		return nil, fmt.Errorf("initial rabbitmq connect: %w", err)
	}

	go c.reconnectLoop(ctx)

	return c, nil
}

func (c *Connection) connect() error {
	conn, err := amqp.DialConfig(c.url, amqp.Config{
		Heartbeat: heartbeatTimeout,
	})
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	return nil
}

// reconnectLoop watches the current connection's close notification and
// redials with a fixed backoff until ctx is cancelled or Close is called.
// On every successful reconnect it fires all registered notifiers so
// consumers/publishers waiting on WaitReconnect wake up and re-declare
// their exchanges/queues/bindings — required because AMQP topology is not
// remembered by the broker across a dropped connection from the client's
// point of view in the general case (a classic queue survives, but the
// client must re-assert bindings to be sure they still match what it expects).
func (c *Connection) reconnectLoop(ctx context.Context) {
	for {
		c.mu.RLock()
		conn := c.conn
		c.mu.RUnlock()

		notifyClose := conn.NotifyClose(make(chan *amqp.Error, 1))

		select {
		case <-ctx.Done():
			return
		case <-c.closeCh:
			return
		case err := <-notifyClose:
			if err != nil {
				c.logger.Error("rabbitmq connection closed, reconnecting", "error", err)
			}
		}

		c.mu.RLock()
		closed := c.closed
		c.mu.RUnlock()
		if closed {
			return
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-c.closeCh:
				return
			case <-time.After(reconnectDelay):
			}

			if err := c.connect(); err != nil {
				c.logger.Error("rabbitmq reconnect failed, retrying", "error", err)
				continue
			}

			c.logger.Info("rabbitmq reconnected")
			c.fireNotifiers()
			break
		}
	}
}

// NotifyReconnect returns a channel that is closed once, the next time
// this Connection successfully reconnects. Callers (typically a Consumer)
// should loop: declare topology, consume until the channel/connection
// dies, call NotifyReconnect again, wait, repeat.
func (c *Connection) NotifyReconnect() <-chan struct{} {
	ch := make(chan struct{})
	c.notifyMu.Lock()
	c.notifiers = append(c.notifiers, ch)
	c.notifyMu.Unlock()
	return ch
}

func (c *Connection) fireNotifiers() {
	c.notifyMu.Lock()
	defer c.notifyMu.Unlock()
	for _, ch := range c.notifiers {
		close(ch)
	}
	c.notifiers = nil
}

// Channel opens a new AMQP channel on the current connection. Callers
// (Publisher, Consumer) must re-call this after a reconnect — channels do
// not survive a connection drop.
func (c *Connection) Channel() (*amqp.Channel, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.conn == nil || c.conn.IsClosed() {
		return nil, fmt.Errorf("rabbitmq connection is not currently open")
	}
	return c.conn.Channel()
}

// Ping opens a channel and immediately closes it to verify the broker is reachable.
func (c *Connection) Ping() error {
	ch, err := c.Channel()
	if err != nil {
		return err
	}
	return ch.Close()
}

// Close stops the reconnect loop and closes the underlying connection.
func (c *Connection) Close() error {
	c.mu.Lock()
	c.closed = true
	conn := c.conn
	c.mu.Unlock()

	close(c.closeCh)

	if conn != nil {
		return conn.Close()
	}
	return nil
}
