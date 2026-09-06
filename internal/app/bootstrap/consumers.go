package bootstrap

import (
	"log/slog"

	"github.com/aasumitro/stratum/internal/platform/messaging"
)

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
