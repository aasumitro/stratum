package billing

import (
	"log/slog"
	"time"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// Dead-letter exchange/queue for every consumer this module owns — see
// Consumers' own doc comment on the handler-owner convention.
const (
	exchangeBillingDLX = "billing.events.dlx"
	exchangeBillingDLQ = "billing.events.dlq"
)

// Consumers builds every RabbitMQ consumer whose handler this module owns —
// mirrors Register's role for HTTP routes. Ownership is handler-owner, not
// exchange-owner: HandleOrganizationCreated lives here because
// billing.Worker runs it, even though it reads from organization's exchange.
func (m *Module) Consumers(mqConn *messaging.Connection, log *slog.Logger) []*messaging.Consumer {
	return []*messaging.Consumer{
		// billing: provision subscription on new organization
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "billing.organization-created",
				BindingKeys:          []string{events.RoutingKeyOrganizationCreated},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeBillingDLX,
				DeadLetterRoutingKey: "billing.organization-created",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeBillingDLX, QueueName: exchangeBillingDLQ},
			PrefetchCount: 10,
		}, m.Worker.HandleOrganizationCreated, log),

		// billing: check subscription expiry (scheduled as an outbox row with a
		// future not_before; the relay publishes it here once it comes due)
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeBilling},
			Queue: messaging.QueueSpec{
				Name:                 "billing.subscription-check",
				BindingKeys:          []string{events.RoutingKeySubscriptionCheck},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeBillingDLX,
				DeadLetterRoutingKey: "billing.subscription-check",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeBillingDLX, QueueName: exchangeBillingDLQ},
			PrefetchCount: 10,
		}, m.Worker.HandleSubscriptionCheck, log),

		// billing: renewal reminder (outbox row with not_before ~7 days before
		// expiry; the relay publishes it here once it comes due)
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeBilling},
			Queue: messaging.QueueSpec{
				Name:                 "billing.subscription-remind",
				BindingKeys:          []string{events.RoutingKeySubscriptionRemind},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeBillingDLX,
				DeadLetterRoutingKey: "billing.subscription-remind",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeBillingDLX, QueueName: exchangeBillingDLQ},
			PrefetchCount: 10,
		}, m.Worker.HandleSubscriptionRemind, log),

		// billing: auto-generate renewal invoice (outbox row with not_before ~3
		// days before expiry; the relay publishes it here once it comes due)
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeBilling},
			Queue: messaging.QueueSpec{
				Name:                 "billing.subscription-auto-invoice",
				BindingKeys:          []string{events.RoutingKeySubscriptionAutoInvoice},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeBillingDLX,
				DeadLetterRoutingKey: "billing.subscription-auto-invoice",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeBillingDLX, QueueName: exchangeBillingDLQ},
			PrefetchCount: 10,
		}, m.Worker.HandleSubscriptionAutoInvoice, log),

		// billing: cancel subscription when organization is deleted
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "billing.organization-deleted",
				BindingKeys:          []string{events.RoutingKeyOrganizationDeleted},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeBillingDLX,
				DeadLetterRoutingKey: "billing.organization-deleted",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeBillingDLX, QueueName: exchangeBillingDLQ},
			PrefetchCount: 10,
		}, m.Worker.HandleOrganizationDeleted, log),
	}
}
