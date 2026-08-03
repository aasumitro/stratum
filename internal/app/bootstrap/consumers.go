package bootstrap

import (
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// Dead-letter exchanges/queues are purely consumer-side infra wiring — the
// publishing modules never need to know about them, unlike the main
// exchanges and routing keys, which live in contracts/events as the single
// source of truth (see events.ExchangeXxx / events.RoutingKeyXxx /
// events.DelayRoutingKeyXxx).
const (
	exchangeBillingDLX      = "billing.events.dlx"
	exchangeBillingDLQ      = "billing.events.dlq"
	exchangeOrganizationDLX = "organization.events.dlx"
	exchangeOrganizationDLQ = "organization.events.dlq"
	exchangeAccountDLX      = "account.events.dlx"
	exchangeAccountDLQ      = "account.events.dlq"
	exchangeNotificationDLX = "notification.events.dlx"
	exchangeNotificationDLQ = "notification.events.dlq"
)

// DeclareWorkerDelayQueues declares the delay parking queue for
// subscription expiry checks. Messages sit here until per-message TTL
// expires, then DLX routes to billing.events. Best-effort like the rest of
// this codebase's startup topology declarations: a failure here (channel
// creation, or any individual declare/bind) is silently skipped rather
// than failing the worker's startup — RabbitMQ topology is expected to
// already exist in any real deployment; this is a dev-convenience/
// idempotent-redeclare, not the source of truth for it.
func DeclareWorkerDelayQueues(mqConn *messaging.Connection) {
	setupCh, err := mqConn.Channel()
	if err != nil {
		return
	}
	defer setupCh.Close()

	_ = setupCh.ExchangeDeclare(events.ExchangeBillingDelay, amqp.ExchangeDirect,
		true, false, false, false, nil)

	delayRoutes := []struct{ routingKey, dlxKey string }{
		{events.DelayRoutingKeySubscriptionCheck, events.RoutingKeySubscriptionCheck},
		{events.DelayRoutingKeySubscriptionRemind, events.RoutingKeySubscriptionRemind},
		{events.DelayRoutingKeySubscriptionAutoInvoice, events.RoutingKeySubscriptionAutoInvoice},
		{events.DelayRoutingKeySubscriptionPaymentRemind, events.RoutingKeySubscriptionPaymentRemind},
		{events.DelayRoutingKeySubscriptionPaymentFinal, events.RoutingKeySubscriptionPaymentFinal},
	}
	for _, r := range delayRoutes {
		queueName := "billing." + r.routingKey + "-delay"
		_, _ = setupCh.QueueDeclare(queueName, true, false, false, false, amqp.Table{
			amqp.QueueTypeArg:           amqp.QueueTypeQuorum,
			"x-dead-letter-exchange":    events.ExchangeBilling,
			"x-dead-letter-routing-key": r.dlxKey,
		})
		_ = setupCh.QueueBind(queueName, r.routingKey, events.ExchangeBillingDelay, false, nil)
	}
}

// NewConsumers builds every RabbitMQ consumer the worker binary runs, each
// bound to its own durable queue with dead-lettering after MaxDeliveries
// failed attempts. See internal/contracts/events for the routing-key
// source of truth.
func NewConsumers(mqConn *messaging.Connection, mods *WorkerModules, log *slog.Logger) []*messaging.Consumer {
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
		}, mods.Billing.Worker.HandleOrganizationCreated, log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleOrganizationCreated), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleInvoicePaid), log),

		// billing: check subscription expiry (delivered via delay queue DLX)
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
		}, mods.Billing.Worker.HandleSubscriptionCheck, log),

		// billing: renewal reminder (delivered via delay queue DLX, 7 days before expiry)
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
		}, mods.Billing.Worker.HandleSubscriptionRemind, log),

		// billing: auto-generate renewal invoice (delivered via delay queue DLX, 3 days before expiry)
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
		}, mods.Billing.Worker.HandleSubscriptionAutoInvoice, log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleSubscriptionRemind), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleInvoiceCreated), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleMemberInvited), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleInvitationRequested), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleInvitationDeclined), log),

		// account: async account deletion (GDPR)
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeAccount},
			Queue: messaging.QueueSpec{
				Name:                 "account.delete-account",
				BindingKeys:          []string{events.RoutingKeyUserDeleteRequest},
				MaxDeliveries:        3,
				RetryMinDelay:        5 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeAccountDLX,
				DeadLetterRoutingKey: "account.delete-account",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeAccountDLX, QueueName: exchangeAccountDLQ},
			PrefetchCount: 5,
		}, mods.Account.Worker.HandleDeleteAccount, log),

		// account: async data export (GDPR)
		messaging.NewConsumer(mqConn, messaging.ConsumerSpec{
			Exchange: messaging.ExchangeSpec{Name: events.ExchangeAccount},
			Queue: messaging.QueueSpec{
				Name:                 "account.export-data",
				BindingKeys:          []string{events.RoutingKeyUserExportRequest},
				MaxDeliveries:        3,
				RetryMinDelay:        5 * time.Second,
				RetryMaxDelay:        60 * time.Second,
				DeadLetterExchange:   exchangeAccountDLX,
				DeadLetterRoutingKey: "account.export-data",
			},
			DLX:           messaging.DLXSpec{ExchangeName: exchangeAccountDLX, QueueName: exchangeAccountDLQ},
			PrefetchCount: 5,
		}, mods.Account.Worker.HandleExportData, log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleUserEmailChanged), log),

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
		}, mods.Billing.Worker.HandleOrganizationDeleted, log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleOrganizationDeleted), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleMemberRemoved), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleMemberRoleChanged), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleOwnershipTransferred), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleWebhookHealthWarning), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleWebhookAutoDisabled), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleInvoiceFailed), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleSubscriptionActivated), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleTrialStarted), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleUsageLimitWarning), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleSubscriptionCancelled), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleSubscriptionExpired), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleSubscriptionResumed), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleSubscriptionPaymentRemind), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleSubscriptionPaymentFinal), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleOrganizationSuspended), log),

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
		}, mods.Notification.Worker.Idempotent(mods.Notification.Worker.HandleOrganizationReactivated), log),

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
		}, mods.Organization.Worker.HandleOutboundEvent, log),

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
		}, mods.Organization.Worker.HandleOutboundEvent, log),

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
		}, mods.Organization.Worker.HandleWebhookRetry, log),

		// organization: purge a deleted organization's logo + files from
		// storage — moved off the synchronous DELETE /organizations/:id
		// request path, same retry/DLQ settings as the billing/notification
		// consumers of this same event.
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
		}, mods.Organization.Worker.HandleOrganizationDeleted, log),
	}
}
