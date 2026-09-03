package notification

import (
	"log/slog"
	"time"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// Dead-letter exchange/queue for every consumer this module owns — see
// Consumers' own doc comment on the handler-owner convention.
const (
	exchangeNotificationDLX = "notification.events.dlx"
	exchangeNotificationDLQ = "notification.events.dlq"
)

// Consumers builds every RabbitMQ consumer whose handler this module owns —
// mirrors Register's role for HTTP routes. Ownership is handler-owner, not
// exchange-owner: most of these read from another module's exchange (e.g.
// organization's or billing's) but live here because notification.Worker
// runs them, and every one dead-letters into this module's own DLX
// regardless of which exchange it's bound to.
func (m *Module) Consumers(mqConn *messaging.Connection, log *slog.Logger) []*messaging.Consumer {
	return []*messaging.Consumer{
		// notification: welcome message on new organization
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "notification.organization-created",
				BindingKeys:          []string{events.RoutingKeyOrganizationCreated},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.organization-created",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleOrganizationCreated), log),

		// notification: invoice paid alert
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeBilling},
			Queue: messaging.QueueSpec{
				Name:                 "notification.invoice-paid",
				BindingKeys:          []string{events.RoutingKeyInvoicePaid},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.invoice-paid",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleInvoicePaid), log),

		// notification: subscription expiring reminder
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeBilling},
			Queue: messaging.QueueSpec{
				Name:                 "notification.subscription-remind",
				BindingKeys:          []string{events.RoutingKeySubscriptionRemind},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.subscription-remind",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleSubscriptionRemind), log),

		// notification: renewal invoice created
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeBilling},
			Queue: messaging.QueueSpec{
				Name:                 "notification.invoice-created",
				BindingKeys:          []string{events.RoutingKeyInvoiceCreated},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.invoice-created",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleInvoiceCreated), log),

		// notification: send invite email
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "notification.member-invited",
				BindingKeys:          []string{events.RoutingKeyMemberInvited},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.member-invited",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleMemberInvited), log),

		// notification: email the original inviter when a lost/expired invite is re-requested
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "notification.invitation-requested",
				BindingKeys:          []string{events.RoutingKeyInvitationRequested},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.invitation-requested",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleInvitationRequested), log),

		// notification: notify the original inviter (in-app) when an invitee declines
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "notification.invitation-declined",
				BindingKeys:          []string{events.RoutingKeyInvitationDeclined},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.invitation-declined",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleInvitationDeclined), log),

		// notification: security notice when account email changes
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeAccount},
			Queue: messaging.QueueSpec{
				Name:                 "notification.email-changed",
				BindingKeys:          []string{events.RoutingKeyUserEmailChanged},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.email-changed",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleUserEmailChanged), log),

		// notification: organization deleted alert to all members
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "notification.organization-deleted",
				BindingKeys:          []string{events.RoutingKeyOrganizationDeleted},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.organization-deleted",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleOrganizationDeleted), log),

		// notification: member removed alert
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "notification.member-removed",
				BindingKeys:          []string{events.RoutingKeyMemberRemoved},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.member-removed",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleMemberRemoved), log),

		// notification: member role changed alert
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "notification.member-role-changed",
				BindingKeys:          []string{events.RoutingKeyMemberRoleChanged},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.member-role-changed",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleMemberRoleChanged), log),

		// notification: member suspended alert (in-app, to the affected member)
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "notification.member-suspended",
				BindingKeys:          []string{events.RoutingKeyMemberSuspended},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.member-suspended",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleMemberSuspended), log),

		// notification: member reinstated alert (in-app, to the affected member)
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "notification.member-reinstated",
				BindingKeys:          []string{events.RoutingKeyMemberReinstated},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.member-reinstated",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleMemberReinstated), log),

		// notification: ownership transferred alert to new owner
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "notification.ownership-transferred",
				BindingKeys:          []string{events.RoutingKeyOwnershipTransferred},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.ownership-transferred",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleOwnershipTransferred), log),

		// notification: webhook endpoint health warning (owner only)
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "notification.webhook-health-warning",
				BindingKeys:          []string{events.RoutingKeyWebhookHealthWarning},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.webhook-health-warning",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleWebhookHealthWarning), log),

		// notification: webhook endpoint auto-disabled (owner only)
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "notification.webhook-auto-disabled",
				BindingKeys:          []string{events.RoutingKeyWebhookAutoDisabled},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.webhook-auto-disabled",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleWebhookAutoDisabled), log),

		// notification: payment failed alert
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeBilling},
			Queue: messaging.QueueSpec{
				Name:                 "notification.invoice-failed",
				BindingKeys:          []string{events.RoutingKeyInvoiceFailed},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.invoice-failed",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleInvoiceFailed), log),

		// notification: subscription activated alert
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeBilling},
			Queue: messaging.QueueSpec{
				Name:                 "notification.subscription-activated",
				BindingKeys:          []string{events.RoutingKeySubscriptionActivated},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.subscription-activated",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleSubscriptionActivated), log),

		// notification: trial started alert
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeBilling},
			Queue: messaging.QueueSpec{
				Name:                 "notification.trial-started",
				BindingKeys:          []string{events.RoutingKeyTrialStarted},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.trial-started",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleTrialStarted), log),

		// notification: usage approaching its plan+addon limit
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeBilling},
			Queue: messaging.QueueSpec{
				Name:                 "notification.usage-limit-warning",
				BindingKeys:          []string{events.RoutingKeyUsageLimitWarning},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.usage-limit-warning",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleUsageLimitWarning), log),

		// notification: subscription cancelled alert
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeBilling},
			Queue: messaging.QueueSpec{
				Name:                 "notification.subscription-cancelled",
				BindingKeys:          []string{events.RoutingKeySubscriptionCancelled},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.subscription-cancelled",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleSubscriptionCancelled), log),

		// notification: subscription expired alert
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeBilling},
			Queue: messaging.QueueSpec{
				Name:                 "notification.subscription-expired",
				BindingKeys:          []string{events.RoutingKeySubscriptionExpired},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.subscription-expired",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleSubscriptionExpired), log),

		// notification: subscription resumed alert
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeBilling},
			Queue: messaging.QueueSpec{
				Name:                 "notification.subscription-resumed",
				BindingKeys:          []string{events.RoutingKeySubscriptionResumed},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.subscription-resumed",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleSubscriptionResumed), log),

		// notification: dunning day-3 payment reminder (via billing.delay DLX)
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeBilling},
			Queue: messaging.QueueSpec{
				Name:                 "notification.subscription-payment-remind",
				BindingKeys:          []string{events.RoutingKeySubscriptionPaymentRemind},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.subscription-payment-remind",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleSubscriptionPaymentRemind), log),

		// notification: dunning day-7 final warning (via billing.delay DLX)
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeBilling},
			Queue: messaging.QueueSpec{
				Name:                 "notification.subscription-payment-final",
				BindingKeys:          []string{events.RoutingKeySubscriptionPaymentFinal},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.subscription-payment-final",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleSubscriptionPaymentFinal), log),

		// notification: organization suspended alert to all members
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "notification.organization-suspended",
				BindingKeys:          []string{events.RoutingKeyOrganizationSuspended},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.organization-suspended",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleOrganizationSuspended), log),

		// notification: organization reactivated alert to all members
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeOrganization},
			Queue: messaging.QueueSpec{
				Name:                 "notification.organization-reactivated",
				BindingKeys:          []string{events.RoutingKeyOrganizationReactivated},
				MaxDeliveries:        5,
				RetryMinDelay:        2 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeNotificationDLX,
				DeadLetterRoutingKey: "notification.organization-reactivated",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeNotificationDLX, QueueName: exchangeNotificationDLQ},
			PrefetchCount: 10,
		}, m.Worker.Idempotent(m.Worker.HandleOrganizationReactivated), log),
	}
}
