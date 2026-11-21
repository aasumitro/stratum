# 07 · Messaging

Anything that doesn't need an immediate answer flows through RabbitMQ. This is what lets the API stay fast while background work happens in the worker.

## Exchanges and the envelope

Each module declares its own topic exchange — nothing is shared. Messages use a standard envelope (`{ID, Type, Source, Time, OrgID, Data}`) and are decoded with a generic helper. Crucially, **every exchange name and routing key lives in exactly one place** — `internal/contracts/events`. Call sites reference those constants; they never hardcode a routing string. (This was consolidated after discovering routing keys duplicated across files, one of which was a latent bug waiting to happen.)

## The worker

`cmd/worker` registers 45 consumers, each bound to a module's queue. When a module publishes an event — an organization is created, an invoice is paid, a member's role changes — the bound consumer reacts: sending a welcome email, firing an in-app notification, fanning out a customer webhook, and so on.

## Delayed messages, without a plugin

Renewal reminders, dunning notices, the end-of-period expiry check, the renewal invoice and a scheduled downgrade's apply all need to fire *in the future*. Rather than depend on a broker plugin or a scheduler, the delay lives entirely in the transactional outbox: `events.EnqueueDelayed` writes the row with a future `not_before`, and the relay only claims a row once `not_before <= now()`. When it comes due the relay publishes it to the real exchange under its final routing key, exactly like an immediate event, and the bound consumer picks it up. The dunning cadence is a day-3 reminder and a day-7 final notice; trials use a 2-day lead.

## Retries and the dead-letter queue

A consumer that fails rejects the message, which is redelivered after a backoff. Once it reaches its queue's delivery limit (five for most queues, three for the expensive ones: GDPR export and deletion, and customer webhook delivery) it dead-letters to its module's DLQ (`<module>.events.dlq`), and the worker's `stratum.dlq.messages` gauge reports it. Consumers are written to tolerate redelivery (idempotent) — for example, subscription provisioning checks for an existing subscription and pending invoice rather than blindly creating duplicates. Operators can inspect, requeue (FIFO), and purge dead-lettered messages from Stratum Studio's queue monitor.

## Outbound customer webhooks

Layered on top of the same event system, the worker also fans out a curated set of nine event types (invoice created/paid/failed; subscription activated/cancelled/expired/resumed; organization created; member invited) to customer-registered webhook endpoints — HMAC-signed, health-tracked, and retryable. The set is deliberately limited to events the worker actually delivers, so a customer can never subscribe to something that would silently never arrive. See the API reference and operations docs for the delivery, health, and secret-rotation details.

## Adding an event
1. Add the routing key to `internal/contracts/events`.
2. Publish it from the owning module's service using the exchange constant — `events.Enqueue`, or `events.EnqueueDelayed` with a delay for a future fire.
3. Declare its consumer in the owning module's `consumers.go`. `bootstrap.NewConsumers` collects every module's consumers, and `bootstrap/consumers_snapshot_test.go` pins the resulting topology, so update its golden entries too.
