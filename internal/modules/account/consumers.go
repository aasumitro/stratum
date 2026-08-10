package account

import (
	"log/slog"
	"time"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// Dead-letter exchange/queue for every consumer this module owns — see
// Consumers' own doc comment on the handler-owner convention.
const (
	exchangeAccountDLX = "account.events.dlx"
	exchangeAccountDLQ = "account.events.dlq"
)

// Consumers builds every RabbitMQ consumer whose handler this module owns —
// mirrors Register's role for HTTP routes.
func (m *Module) Consumers(mqConn *messaging.Connection, log *slog.Logger) []*messaging.Consumer {
	return []*messaging.Consumer{
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
		}, m.Worker.HandleDeleteAccount, log),

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
		}, m.Worker.HandleExportData, log),
	}
}
