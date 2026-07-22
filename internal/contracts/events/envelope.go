package events

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/aasumitro/stratum/internal/platform/logger"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// Exchange names — the single source of truth for every RabbitMQ exchange
// a module publishes to. Both the publishing module (via events.Publish/
// PublishDelayed) and the consumer wiring (internal/app/worker.go) must
// reference these, never redeclare the string locally.
const (
	ExchangeOrganization = "organization.events"
	ExchangeBilling      = "billing.events"
	ExchangeBillingDelay = "billing.delay"
	ExchangeAccount      = "account.events"
)

// Envelope wraps every domain event published to RabbitMQ. Modeling this
// after the CloudEvents spec (loosely — we don't pull in the CloudEvents
// SDK) means the wire format is already close to what most managed
// messaging platforms and observability tools expect, which matters once
// this crosses a process boundary after extraction.
//
// Data is left as json.RawMessage-equivalent (any) rather than a typed
// field because the envelope is constructed generically by the publisher;
// callers unmarshal Data into the concrete event type (e.g. OrgCreated)
// indicated by Type.
type Envelope struct {
	ID     string    `json:"id"`     // unique event ID (uuidv7) for idempotency/dedup downstream
	Type   string    `json:"type"`   // e.g. "org.created", "billing.subscription.activated"
	Source string    `json:"source"` // publishing module, e.g. "organization", "billing"
	Time   time.Time `json:"time"`
	OrgID  string    `json:"org_id,omitempty"` // present on nearly all events; lets consumers filter/route by organization
	Data   any       `json:"data"`
}

// Publish wraps data in an Envelope and publishes it. Fire-and-forget:
// logs on failure instead of returning an error, since event publishing
// must not block the primary write path.
func Publish(ctx context.Context, pub messaging.EventPublisher, exchange, routingKey, source, orgID string, data any) {
	env := Envelope{
		ID:     uuid.New().String(),
		Type:   routingKey,
		Source: source,
		Time:   time.Now(),
		OrgID:  orgID,
		Data:   data,
	}
	body, err := json.Marshal(env)
	if err != nil {
		logger.FromContext(ctx).Error("failed to marshal event", "routing_key", routingKey, "error", err)
		return
	}
	if err := pub.Publish(ctx, exchange, routingKey, body); err != nil {
		logger.FromContext(ctx).Warn("event publish failed", "exchange", exchange, "routing_key", routingKey, "error", err)
	}
}

// Decode unmarshals an Envelope and then the typed Data field.
func Decode[T any](body []byte) (T, error) {
	var zero T
	var env Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return zero, err
	}
	raw, err := json.Marshal(env.Data)
	if err != nil {
		return zero, err
	}
	var evt T
	if err := json.Unmarshal(raw, &evt); err != nil {
		return zero, err
	}
	return evt, nil
}

// PublishDelayed wraps data in an Envelope and publishes with a per-message TTL.
// Used with a parking queue + DLX for delayed delivery (e.g. subscription expiry checks).
func PublishDelayed(ctx context.Context, pub messaging.EventPublisher, exchange, routingKey, source, orgID string, data any, delay time.Duration) {
	env := Envelope{
		ID:     uuid.New().String(),
		Type:   routingKey,
		Source: source,
		Time:   time.Now(),
		OrgID:  orgID,
		Data:   data,
	}
	body, err := json.Marshal(env)
	if err != nil {
		logger.FromContext(ctx).Error("failed to marshal delayed event", "routing_key", routingKey, "error", err)
		return
	}
	if err := pub.PublishDelayed(ctx, exchange, routingKey, body, delay); err != nil {
		logger.FromContext(ctx).Warn("delayed event publish failed", "exchange", exchange, "routing_key", routingKey, "error", err)
	}
}
