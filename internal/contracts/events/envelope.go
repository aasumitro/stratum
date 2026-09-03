package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"uuid"

	"github.com/aasumitro/stratum/internal/platform/db"
	"github.com/aasumitro/stratum/internal/platform/outbox"
)

// Exchange names — the single source of truth for every RabbitMQ exchange
// a module publishes to. Both the publishing module (via events.Enqueue/
// EnqueueDelayed) and the consumer wiring (internal/app/worker.go) must
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

// EnvelopeID reads just the id field of a wire-format Envelope, without
// decoding the typed Data payload — for callers that need the event's
// identity ahead of knowing (or caring about) its concrete type, e.g. an
// idempotency check that must run before dispatch.
func EnvelopeID(body []byte) (string, error) {
	var env struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return "", err
	}
	return env.ID, nil
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

// Enqueue writes env to the transactional outbox inside q's transaction —
// the caller is responsible for q being the same transaction as the state
// change the event describes, so both commit or neither does. A failure
// here must fail the caller's transaction: an outbox row that silently
// fails to insert reopens a fire-and-forget delivery gap. Envelope
// construction and marshaling live here
// (it's shared, cross-module vocabulary); the actual row write is
// internal/platform/outbox's job, since persistence logic doesn't belong
// next to a shared type/interface package. A cmd/worker relay (also in
// internal/platform/outbox) delivers the row to the broker afterward,
// independent of this insert's caller.
func Enqueue(
	ctx context.Context, q db.Querier,
	exchange, routingKey, source, orgID string, data any,
) error {
	env := Envelope{
		ID: uuid.NewV7().String(), Type: routingKey, Source: source,
		Time: time.Now(), OrgID: orgID, Data: data,
	}
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("events.Enqueue: marshal: %w", err)
	}
	if err := outbox.Enqueue(ctx, q, env.ID, exchange, routingKey, body); err != nil {
		return fmt.Errorf("events.Enqueue: %w", err)
	}
	return nil
}

// EnqueueDelayed is Enqueue with not_before set in the future, unifying
// "publish now" and "publish later" into the relay's single
// not_before <= now() query instead of a separate delayed-outbox mechanism.
func EnqueueDelayed(
	ctx context.Context, q db.Querier,
	exchange, routingKey, source, orgID string, data any, delay time.Duration,
) error {
	env := Envelope{
		ID: uuid.NewV7().String(), Type: routingKey, Source: source,
		Time: time.Now(), OrgID: orgID, Data: data,
	}
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("events.EnqueueDelayed: marshal: %w", err)
	}
	if err := outbox.EnqueueDelayed(ctx, q, env.ID, exchange, routingKey, body, delay); err != nil {
		return fmt.Errorf("events.EnqueueDelayed: %w", err)
	}
	return nil
}
