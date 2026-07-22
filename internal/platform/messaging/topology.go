package messaging

import (
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ExchangeSpec describes one durable topic exchange a module publishes
// to. Each module declares exactly one of these (e.g. "org.events",
// "billing.events") — see contracts/events for the routing keys
// published onto it.
type ExchangeSpec struct {
	Name string // e.g. "org.events"
}

// QueueSpec describes one durable quorum queue a consumer reads from,
// including its native delayed-retry behavior (RabbitMQ 4.3+) and
// dead-letter routing for messages that exhaust their retry attempts.
//
// Retry behavior: when a delivery actually fails (consumer calls
// Delivery.Reject, or the channel/session terminates with the delivery
// still unacknowledged — see consumer.go's use of Reject rather than
// Nack), the broker redelivers it after a delay that grows linearly
// between RetryMinDelay and RetryMaxDelay, based on the message's
// delivery-count, up to MaxDeliveries attempts. After MaxDeliveries, the
// message is routed to DeadLetterExchange/DeadLetterRoutingKey instead of
// being redelivered again — this is the "poison message" escape hatch so
// one bad message can't block a queue forever.
//
// We deliberately use retry-type "failed" (not "returned" or "all"):
// "failed" only delays redeliveries that increment delivery-count, which
// keeps the delay mechanism and the delivery-limit mechanism counting the
// same thing. "returned" delays explicit application-level returns that
// do NOT count toward the limit — useful for app-level routing, not for
// our "retry then dead-letter" use case.
type QueueSpec struct {
	Name        string
	BindingKeys []string // routing key patterns to bind, e.g. "org.created", "billing.*"

	MaxDeliveries        int           // x-delivery-limit: attempts before dead-lettering
	RetryMinDelay        time.Duration // x-delayed-retry-min
	RetryMaxDelay        time.Duration // x-delayed-retry-max
	DeadLetterExchange   string        // module's DLX, e.g. "org.events.dlx"
	DeadLetterRoutingKey string        // typically same as Name, so the DLQ shows which queue a message died in
}

// DLXSpec describes the dead-letter exchange + queue pair that catches
// messages from every QueueSpec in a module after MaxDeliveries is
// exhausted. One DLX/DLQ per module (not per queue) keeps the failure
// inspection surface small — an operator checks one DLQ per module
// rather than one per consumer.
type DLXSpec struct {
	ExchangeName string // e.g. "org.events.dlx"
	QueueName    string // e.g. "org.events.dlq"
}

// DeclareExchange declares a durable topic exchange. Safe to call on
// every startup and after every reconnect — ExchangeDeclare is
// idempotent as long as the arguments match what's already declared.
func DeclareExchange(ch *amqp.Channel, spec ExchangeSpec) error {
	err := ch.ExchangeDeclare(
		spec.Name,
		amqp.ExchangeTopic,
		true,  // durable
		false, // autoDelete
		false, // internal
		false, // noWait
		nil,   // args
	)
	if err != nil {
		return fmt.Errorf("declaring exchange %q: %w", spec.Name, err)
	}
	return nil
}

// DeclareDLX declares a module's dead-letter exchange and queue, and
// binds the queue to the exchange with a wildcard so every routing key
// dead-lettered into this exchange lands in the one DLQ.
func DeclareDLX(ch *amqp.Channel, spec DLXSpec) error {
	if err := ch.ExchangeDeclare(
		spec.ExchangeName,
		amqp.ExchangeTopic,
		true, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("declaring dlx %q: %w", spec.ExchangeName, err)
	}

	if _, err := ch.QueueDeclare(
		spec.QueueName,
		true,  // durable
		false, // autoDelete
		false, // exclusive
		false, // noWait
		amqp.Table{amqp.QueueTypeArg: amqp.QueueTypeQuorum},
	); err != nil {
		return fmt.Errorf("declaring dlq %q: %w", spec.QueueName, err)
	}

	if err := ch.QueueBind(spec.QueueName, "#", spec.ExchangeName, false, nil); err != nil {
		return fmt.Errorf("binding dlq %q to dlx %q: %w", spec.QueueName, spec.ExchangeName, err)
	}

	return nil
}

// DeclareQueue declares a durable quorum queue with native delayed retry
// and dead-letter routing, then binds it to exchangeName for every
// pattern in spec.BindingKeys.
//
// Queue arguments reference: RabbitMQ 4.3+ quorum queue delayed retry
// (x-delayed-retry-type/min/max) plus the classic x-delivery-limit and
// x-dead-letter-exchange/x-dead-letter-routing-key combination used to
// dead-letter a message once retries are exhausted.
func DeclareQueue(ch *amqp.Channel, exchangeName string, spec QueueSpec) error {
	args := amqp.Table{
		amqp.QueueTypeArg:        amqp.QueueTypeQuorum,
		"x-delivery-limit":       spec.MaxDeliveries,
		"x-delayed-retry-type":   "failed", // see QueueSpec doc: only delays redeliveries that also count toward x-delivery-limit
		"x-delayed-retry-min":    spec.RetryMinDelay.Milliseconds(),
		"x-delayed-retry-max":    spec.RetryMaxDelay.Milliseconds(),
		"x-dead-letter-exchange": spec.DeadLetterExchange,
	}
	if spec.DeadLetterRoutingKey != "" {
		args["x-dead-letter-routing-key"] = spec.DeadLetterRoutingKey
	}

	if _, err := ch.QueueDeclare(
		spec.Name,
		true,  // durable
		false, // autoDelete
		false, // exclusive
		false, // noWait
		args,
	); err != nil {
		return fmt.Errorf("declaring queue %q: %w", spec.Name, err)
	}

	for _, key := range spec.BindingKeys {
		if err := ch.QueueBind(spec.Name, key, exchangeName, false, nil); err != nil {
			return fmt.Errorf("binding queue %q to %q with key %q: %w", spec.Name, exchangeName, key, err)
		}
	}

	return nil
}
