package organization

import (
	"log/slog"
	"time"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// Dead-letter exchange/queue for every consumer this module owns — see
// Consumers' own doc comment on the handler-owner convention.
const (
	exchangeOrganizationDLX = "organization.events.dlx"
	exchangeOrganizationDLQ = "organization.events.dlq"
)

// Consumers builds every RabbitMQ consumer whose handler this module owns —
// mirrors Register's role for HTTP routes. Ownership is handler-owner, not
// exchange-owner: a consumer lives here because organization.Worker runs
// it, even where (as with the outbound-webhook fan-out below) it reads from
// another module's exchange.
func (m *Module) Consumers(mqConn *messaging.Connection, log *slog.Logger) []*messaging.Consumer {
	return []*messaging.Consumer{
		// outbound webhooks: fan out billing events to customer webhook endpoints
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeBilling},
			Queue: messaging.QueueSpec{
				Name: "organization.webhook-billing",
				BindingKeys: []string{
					events.RoutingKeyInvoiceCreated,
					events.RoutingKeyInvoicePaid,
					events.RoutingKeyInvoiceFailed,
					events.RoutingKeySubscriptionActivated,
					events.RoutingKeySubscriptionCancelled,
					events.RoutingKeySubscriptionExpired,
					events.RoutingKeySubscriptionResumed,
				},
				MaxDeliveries:        3,
				RetryMinDelay:        5 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeOrganizationDLX,
				DeadLetterRoutingKey: "organization.webhook-billing",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeOrganizationDLX, QueueName: exchangeOrganizationDLQ},
			PrefetchCount: 10,
		}, m.Worker.HandleOutboundEvent, log),

		// outbound webhooks: fan out organization events to customer webhook endpoints
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name: "organization.webhook-organization",
				BindingKeys: []string{
					events.RoutingKeyOrganizationCreated,
					events.RoutingKeyMemberInvited,
				},
				MaxDeliveries:        3,
				RetryMinDelay:        5 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeOrganizationDLX,
				DeadLetterRoutingKey: "organization.webhook-organization",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeOrganizationDLX, QueueName: exchangeOrganizationDLQ},
			PrefetchCount: 10,
		}, m.Worker.HandleOutboundEvent, log),

		// outbound webhooks: process one "retry all failed" job at a time —
		// the bulk retry action dispatches one of these per delivery instead
		// of retrying inline on the request goroutine; PrefetchCount: 1
		// keeps a single organization's bulk retry from monopolizing every
		// worker goroutine at once.
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "organization.webhook-retry",
				BindingKeys:          []string{events.RoutingKeyWebhookRetryRequested},
				MaxDeliveries:        3,
				RetryMinDelay:        5 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeOrganizationDLX,
				DeadLetterRoutingKey: "organization.webhook-retry",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeOrganizationDLX, QueueName: exchangeOrganizationDLQ},
			PrefetchCount: 1,
		}, m.Worker.HandleWebhookRetry, log),

		// organization: purge a deleted organization's logo from storage —
		// moved off the synchronous DELETE /organizations/:id request path,
		// same retry/DLQ settings as the billing/notification consumers of
		// this same event.
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "organization.organization-deleted",
				BindingKeys:          []string{events.RoutingKeyOrganizationDeleted},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeOrganizationDLX,
				DeadLetterRoutingKey: "organization.organization-deleted",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeOrganizationDLX, QueueName: exchangeOrganizationDLQ},
			PrefetchCount: 10,
		}, m.Worker.HandleOrganizationDeleted, log),
	}
}
