package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// DeclareWorkerDelayQueues declares the delay parking queue for
// subscription expiry checks. Messages sit here until per-message TTL
// expires, then DLX routes to billing.events. Returns a wrapped error on
// any channel/declare/bind failure instead of swallowing it — the caller
// decides whether that's fatal (first boot, see RunWorker) or something to
// retry (a redeclare after reconnect, see RunDelayTopologyReconnectLoop).
// Idempotent: re-declaring with the same arguments against an already-correct
// broker is a no-op; re-declaring against a *differently* configured
// existing exchange/queue correctly fails loudly (RabbitMQ's own
// PRECONDITION_FAILED), which is exactly the case that must not be silently
// swallowed.
func DeclareWorkerDelayQueues(mqConn *messaging.Connection) error {
	setupCh, err := mqConn.Channel()
	if err != nil {
		return fmt.Errorf("bootstrap.DeclareWorkerDelayQueues: open channel: %w", err)
	}
	defer func() { _ = setupCh.Close() }()

	if err := setupCh.ExchangeDeclare(events.ExchangeBillingDelay, amqp.ExchangeDirect,
		true, false, false, false, nil); err != nil {
		return fmt.Errorf("bootstrap.DeclareWorkerDelayQueues: declare exchange %q: %w", events.ExchangeBillingDelay, err)
	}

	delayRoutes := []struct{ routingKey, dlxKey string }{
		{events.DelayRoutingKeySubscriptionCheck, events.RoutingKeySubscriptionCheck},
		{events.DelayRoutingKeySubscriptionRemind, events.RoutingKeySubscriptionRemind},
		{events.DelayRoutingKeySubscriptionAutoInvoice, events.RoutingKeySubscriptionAutoInvoice},
		{events.DelayRoutingKeySubscriptionPaymentRemind, events.RoutingKeySubscriptionPaymentRemind},
		{events.DelayRoutingKeySubscriptionPaymentFinal, events.RoutingKeySubscriptionPaymentFinal},
	}
	for _, r := range delayRoutes {
		queueName := "billing." + r.routingKey + "-delay"
		if _, err := setupCh.QueueDeclare(
			queueName,
			true,
			false,
			false,
			false,
			amqp.Table{
				amqp.QueueTypeArg:           amqp.QueueTypeQuorum,
				"x-dead-letter-exchange":    events.ExchangeBilling,
				"x-dead-letter-routing-key": r.dlxKey,
			},
		); err != nil {
			return fmt.Errorf("bootstrap.DeclareWorkerDelayQueues: declare queue %q: %w", queueName, err)
		}
		if err := setupCh.QueueBind(
			queueName, r.routingKey,
			events.ExchangeBillingDelay,
			false, nil,
		); err != nil {
			return fmt.Errorf("bootstrap.DeclareWorkerDelayQueues: bind queue %q: %w", queueName, err)
		}
	}
	return nil
}

// RunDelayTopologyReconnectLoop redeclares the delay-exchange topology after
// every RabbitMQ reconnect, mirroring Consumer.Run's self-healing shape.
// The delay parking queues have no consumer of their own to piggyback this
// on — nothing consumes them directly, RabbitMQ's own TTL+DLX moves messages
// out — so without this loop a topology loss after a successful boot (an
// operator dropping the exchange, a broker migration) would never
// self-heal the way every other consumer-owned queue in this codebase does.
// Blocks until ctx is done; call it in its own goroutine.
func RunDelayTopologyReconnectLoop(ctx context.Context, mqConn *messaging.Connection, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-mqConn.NotifyReconnect():
			if err := DeclareWorkerDelayQueues(mqConn); err != nil {
				log.Error("failed to redeclare worker delay topology after reconnect", "error", err)
				continue
			}
			log.Info("worker delay topology redeclared after reconnect")
		}
	}
}

// NewConsumers aggregates every RabbitMQ consumer the worker binary runs.
// Each module owns its own consumer definitions via its Consumers method
// (mirroring Register's role for HTTP routes) — this function no longer
// declares any ConsumerSpec itself, it only concatenates what each module
// reports. Ownership convention: a consumer belongs to the module whose
// handler runs it (handler-owner), not the module whose exchange it reads
// from — e.g. billing.Consumers includes a consumer bound to
// organization's exchange, because billing.Worker is what handles it.
// See internal/contracts/events for the routing-key source of truth, and
// each module's own consumers.go for its topology.
func NewConsumers(mqConn *messaging.Connection, mods *WorkerModules, log *slog.Logger) []*messaging.Consumer {
	var consumers []*messaging.Consumer
	consumers = append(consumers, mods.Organization.Consumers(mqConn, log)...)
	consumers = append(consumers, mods.Account.Consumers(mqConn, log)...)
	consumers = append(consumers, mods.Billing.Consumers(mqConn, log)...)
	consumers = append(consumers, mods.Notification.Consumers(mqConn, log)...)
	return consumers
}
