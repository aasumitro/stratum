# 12 · Operations

How Stratum is deployed, kept healthy, and recovered when something goes wrong.

## Topology
Two Go binaries (`api` and `worker`) run alongside three infrastructure services — PostgreSQL 18, RabbitMQ 4, and Redis 8 (containers; `deploy/docker-compose.local.yml`). An OpenTelemetry collector is optional (`deploy/otel/`). External dependencies are Supabase (auth + storage), Stripe/Xendit (payments), and an SMTP server (email).

```
Web ──HTTPS──► api ──► Postgres / Redis
                 └─publish─► RabbitMQ ──► worker ──► Postgres / SMTP / Stripe·Xendit
Studio ──direct DSN──► Postgres / RabbitMQ / Redis   (bypasses the API)
```

## Configuration
All configuration is read from the environment once at startup and passed explicitly through the app — never read deep inside a module. The required values are the three infra URLs plus the JWKS URL and issuer; payments, SMTP, storage, and telemetry are optional and degrade gracefully when unset. Secrets live in `.env.local` and are never committed or logged. See `02-getting-started.md` for the full variable list.

## Deploy sequence
```bash
cp .env.example .env      # fill required values
make infra-up
# provision database roles here — staging/production only, see
# "Provisioning database roles" below (local dev already did this
# automatically when the Postgres container first booted)
make migrate-up
make build                # bin/api + bin/worker
make run-api
make run-worker
```

**Provisioning database roles**: role creation is deliberately not part of the migration
pipeline (`ADR-0026`) — `db/migrations/000008` only grants privileges to `stratum_app`,
`stratum_worker`, and `stratum_webhook`; it never creates them. This is what lets
`make migrate-up` run without the migrator role itself needing `BYPASSRLS` or `CREATEROLE` —
PostgreSQL only permits a role that already holds `BYPASSRLS` to grant `BYPASSRLS` to a role
it creates, so embedding `CREATE ROLE ... BYPASSRLS` inside a migration would force every
migrator, including a deliberately unprivileged production one, to hold it too.

Local dev and CI get this for free: `deploy/postgres-init/01-app-role.sql` creates all three
roles, and either runs automatically (local dev, mounted into the Postgres container's
`docker-entrypoint-initdb.d`) or as an explicit CI step before migrations. Every other
environment needs a human to run the equivalent once, with a superuser-equivalent connection,
**before the first `make migrate-up`** — copy `deploy/postgres-init/01-app-role.sql`'s three
`CREATE ROLE` statements, substituting real generated passwords for its hardcoded local-dev
ones:
```sql
CREATE ROLE stratum_app LOGIN PASSWORD '...';
CREATE ROLE stratum_worker LOGIN PASSWORD '...' BYPASSRLS;
CREATE ROLE stratum_webhook LOGIN PASSWORD '...' BYPASSRLS;
```
If this step is skipped, `make migrate-up` fails immediately and clearly on `000008`'s first
`GRANT ... TO stratum_app` with `role "stratum_app" does not exist` — run the step above, then
retry.

**Upgrading an existing deployment**: `POSTGRES_URL` (the app's old single runtime DSN) is
replaced by three role-scoped DSNs — `POSTGRES_APP_URL`, `POSTGRES_WORKER_URL`,
`POSTGRES_WEBHOOK_URL` — and `WEBHOOK_SECRET_ENCRYPTION_KEY` (required) plus `STATS_TOKEN`
(recommended) are now required/expected outside development; `config.Load()` fails startup
if `WEBHOOK_SECRET_ENCRYPTION_KEY` is unset or empty. `POSTGRES_URL` itself is unchanged as
the admin DSN `make migrate-up` runs against (see `DATABASE_URL ?= $(POSTGRES_URL)` in the
Makefile) — only the app's own runtime connections moved. Apply in this order:
1. Provisioning database roles (above), if not already done for this environment — migrations
   no longer create the `stratum_app`/`stratum_worker`/`stratum_webhook` roles as part of their
   DDL, so this step must complete before the next one, not as a side effect of it.
2. Run `make migrate-up` against `POSTGRES_URL`, using a role that owns the schemas (no longer
   needs `BYPASSRLS` or `CREATEROLE`).
3. Set `POSTGRES_APP_URL`, `POSTGRES_WORKER_URL`, `POSTGRES_WEBHOOK_URL`,
   `WEBHOOK_SECRET_ENCRYPTION_KEY`, and `STATS_TOKEN` in the environment for `api` and
   `worker`.
4. Start `api` and `worker` only after steps 1-3 complete.

Storage buckets auto-create on startup. Verify with `GET /health/ready` (expect 200) and confirm the worker registered its consumers.

## Health and observability
Three endpoints: `/health` (liveness), `/health/ready` (readiness — returns 503 when a dependency is degraded), and `/health/stats` (goroutines, memory, GC, connection-pool stats). `/health` and `/health/ready` are always safe to expose publicly; `/health/stats` leaks process internals and is gated by the `STATS_TOKEN` env var — set it (any non-empty value) and enter the same value in Studio's project form so its monitoring page can keep polling it. An empty `STATS_TOKEN` leaves the endpoint open (fine for local dev, logged as a startup warning outside development). Telemetry (traces, metrics, logs) is exported via OpenTelemetry when a collector URL is configured, and is a no-op otherwise. Stratum Studio's monitoring page polls the health endpoints and keeps history.

## Backups and data
PostgreSQL is the system of record — back it up per your SLA (define RPO/RTO here for your deployment). Redis is disposable cache (roles, rate-limit counters, the token blocklist) — losing it degrades, it doesn't corrupt. RabbitMQ holds in-flight events; a dead-letter backlog is recoverable. Supabase Storage holds user files and should be backed up separately if required.

## Runbooks

**Dead-letter backlog** — In Studio's queue monitor, inspect the messages, fix the root cause, then requeue (FIFO) or purge poison messages. Consumers are idempotent, so requeue is safe for a *transient* failure. One `billing.events.dlq` cause is not transient: an `organization.created` event whose cart-selected coupon was exhausted by someone else before provisioning ran — the organization exists (its create request already succeeded) but has no subscription. Requeuing retries the same now-exhausted coupon and fails identically; the fix is to provision the subscription manually via Studio (without the coupon, or with a different one) and purge the dead-lettered event, not requeue it. No alert currently fires when this happens — it's only visible by checking the queue.

**Failing customer webhook** — Endpoints auto-disable after a three-day, 100%-failure window and warn below 70% success over 24 hours. Re-enable is only possible via a passing test event, not a direct toggle. Use the deliveries panel to diagnose, then bulk-retry failed deliveries once the receiver is fixed.

**Payment reconciliation** — Billing state only advances on a verified gateway webhook. If a customer paid but nothing changed, check that the webhook was received (and its signing secret), then use Studio's support tools to mark the invoice paid as a manual override (which is logged).

**Degraded readiness** — A 503 from `/health/ready` means a dependency check failed; `/health/stats` shows pool and GC pressure. Check Postgres, Redis, and RabbitMQ reachability. Don't restart infrastructure from inside an automated session — surface it to a human.

**Stuck outbox backlog** — Every domain event is written to `messaging.outbox` in the same transaction as the state change it describes, then delivered to RabbitMQ by a `cmd/worker` relay polling every 2 seconds. This alert and the next one are two distinct conditions — check `attempts` before concluding which one you're looking at; a row that has exhausted its retries (`attempts >= 20`) is not "delayed", it is abandoned and will never deliver.

*Delivery delayed, nothing lost* — rows still retrying:
```sql
SELECT count(*) FROM messaging.outbox WHERE published_at IS NULL AND attempts < 20 AND created_at < now() - interval '5 minutes';
```
A nonzero result means the relay is stuck (worker process down or wedged) or the broker is unreachable — these events are durably queued and will deliver once the relay recovers. Check the worker process is running and can reach RabbitMQ; `attempts`/`last_error` on the oldest unpublished rows show the last failure the relay recorded for them.

*Events abandoned, manual replay required* — rows that exhausted every retry:
```sql
SELECT count(*) FROM messaging.outbox WHERE published_at IS NULL AND attempts >= 20;
```
A nonzero result here means these events were **not** delivered and never will be without operator action — the relay gave up on them (a malformed payload, a permanently-unroutable key), logged each one at `ERROR` (`"outbox: row exhausted, ABANDONED — will never be delivered"`), and moved on. Nothing is queued for later delivery. Inspect `last_error` on the affected rows, fix the underlying cause, then replay:
```sql
UPDATE messaging.outbox SET attempts = 0, not_before = now() WHERE id = ANY(:ids);
```

**Rotating the webhook encryption key** — `WEBHOOK_SECRET_ENCRYPTION_KEY` rotation is a
4-step, operator-driven procedure; do not skip the verification step. Do not roll back the
application between steps 1 and 3 once the backfill has started — a rollback to the pre-rotation
release makes every already-rotated row fail to decrypt (`_PREVIOUS` is unset on that release, so
the `ELSE` branch of the key-selection `CASE` gets an empty passphrase and `pgcrypto` hard-errors).
Roll forward instead.

1. Set `WEBHOOK_SECRET_ENCRYPTION_KEY_PREVIOUS` to the current (soon-to-be-old) key, bump
   `WEBHOOK_SECRET_ENCRYPTION_KEY_VERSION` by one, set `WEBHOOK_SECRET_ENCRYPTION_KEY` to
   the new key, and deploy.
2. Run the backfill query against `POSTGRES_URL` (replace `:old_key`, `:new_key`,
   `:new_version` with the actual rotation values):
   ```sql
   UPDATE organization.webhook_endpoints
   SET secret_encrypted = pgp_sym_encrypt(pgp_sym_decrypt(secret_encrypted, :old_key), :new_key),
       secret_encrypted_previous = CASE WHEN secret_encrypted_previous IS NULL THEN NULL
           ELSE pgp_sym_encrypt(pgp_sym_decrypt(secret_encrypted_previous, :old_key), :new_key) END,
       key_version = :new_version
   WHERE key_version != :new_version;
   ```
3. Verify every row converged before proceeding:
   ```sql
   SELECT count(*) FROM organization.webhook_endpoints WHERE key_version != :new_version;
   ```
   Only proceed to step 4 once this returns `0`.
4. Clear `WEBHOOK_SECRET_ENCRYPTION_KEY_PREVIOUS` and redeploy.

**Stuck webhook-driven payment confirmation** — Any environment that ran the `FORCE ROW LEVEL SECURITY` migration before `stratum_webhook` (the dedicated `BYPASSRLS` role webhook processing now runs on) shipped may have invoices a customer actually paid for, where the provider's webhook silently failed to apply — the webhook routes carry no authenticated org context, so a role still bound by RLS can't see the rows it needs to update. Find candidates:
```sql
SELECT i.id, i.status, pl.external_id, pl.provider, s.subject_id
FROM billing.invoices i
JOIN billing.payment_links pl ON pl.invoice_id = i.id
JOIN billing.subscriptions s ON s.id = i.subscription_id
WHERE i.status = 'pending' AND pl.status = 'pending' AND i.created_at < now() - interval '1 hour';
```
Each result needs manual verification against the payment provider's own dashboard (Stripe/Xendit) before marking anything paid by hand via Studio's support tools — a row in this list only means the invoice looks stale, not that it was actually paid.

## No scheduler
Stratum has no cron. Anything that looks periodic is either a one-shot RabbitMQ delayed message (dunning, renewal reminders) or lazy/opportunistic work (webhook health computed on delivery). This is a deliberate simplification — adding a scheduler for a single sweep wasn't judged worth the operational surface.
