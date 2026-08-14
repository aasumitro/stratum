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
make migrate-up
make build                # bin/api + bin/worker
make run-api
make run-worker
```

**BYPASSRLS requirement**: `db/migrations/000008` creates the `stratum_app`,
`stratum_worker`, and `stratum_webhook` roles with `BYPASSRLS`, which PostgreSQL only
permits when the role executing the migration itself already holds `BYPASSRLS`. This is
true by accident in local dev/CI (the Postgres container image's default superuser) but is
not true in a hardened production setup, where the role running `make migrate-up` is
deliberately not a superuser. In that case, pre-create the three roles with a
superuser-equivalent connection first, mirroring `deploy/postgres-init/01-app-role.sql`'s
shape (with real production passwords instead of that script's hardcoded local ones) —
the migrations' own `CREATE ROLE ... IF NOT EXISTS` blocks are then no-ops. Do this before
running `make migrate-up` in production.

**Password Provisioning**: In staging and production environments, the `stratum_app`, `stratum_worker`, and `stratum_webhook` roles created by the migrations start with empty/invalid passwords. You must explicitly provision them from your secrets manager:
```sql
ALTER ROLE stratum_app WITH PASSWORD '...';
ALTER ROLE stratum_worker WITH PASSWORD '...';
ALTER ROLE stratum_webhook WITH PASSWORD '...';
```

**Upgrading an existing deployment**: `POSTGRES_URL` (the app's old single runtime DSN) is
replaced by three role-scoped DSNs — `POSTGRES_APP_URL`, `POSTGRES_WORKER_URL`,
`POSTGRES_WEBHOOK_URL` — and `WEBHOOK_SECRET_ENCRYPTION_KEY` (required) plus `STATS_TOKEN`
(recommended) are now required/expected outside development; `config.Load()` fails startup
if `WEBHOOK_SECRET_ENCRYPTION_KEY` is unset or empty. `POSTGRES_URL` itself is unchanged as
the admin DSN `make migrate-up` runs against (see `DATABASE_URL ?= $(POSTGRES_URL)` in the
Makefile) — only the app's own runtime connections moved. Apply in this order:
1. Run `make migrate-up` against `POSTGRES_URL`, using a role that already holds the
   database roles/attributes the migrations need — this creates/updates the `stratum_app`,
   `stratum_worker`, and `stratum_webhook` roles as part of its DDL.
2. Provision the three roles' passwords (see Password Provisioning above).
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

**Dead-letter backlog** — In Studio's queue monitor, inspect the messages, fix the root cause, then requeue (FIFO) or purge poison messages. Consumers are idempotent, so requeue is safe.

**Failing customer webhook** — Endpoints auto-disable after a three-day, 100%-failure window and warn below 70% success over 24 hours. Re-enable is only possible via a passing test event, not a direct toggle. Use the deliveries panel to diagnose, then bulk-retry failed deliveries once the receiver is fixed.

**Payment reconciliation** — Billing state only advances on a verified gateway webhook. If a customer paid but nothing changed, check that the webhook was received (and its signing secret), then use Studio's support tools to mark the invoice paid as a manual override (which is logged).

**Degraded readiness** — A 503 from `/health/ready` means a dependency check failed; `/health/stats` shows pool and GC pressure. Check Postgres, Redis, and RabbitMQ reachability. Don't restart infrastructure from inside an automated session — surface it to a human.

**Stuck outbox backlog** — Every domain event is written to `messaging.outbox` in the same transaction as the state change it describes, then delivered to RabbitMQ by a `cmd/worker` relay polling every 2 seconds. Alert on:
```sql
SELECT count(*) FROM messaging.outbox WHERE published_at IS NULL AND created_at < now() - interval '5 minutes';
```
A nonzero result means the relay is stuck (worker process down or wedged) or the broker is unreachable — events are durably queued (nothing is lost), but delivery is delayed. Check the worker process is running and can reach RabbitMQ; `attempts`/`last_error` on the oldest unpublished rows show the last failure the relay recorded for them.

**Rotating the webhook encryption key** — `WEBHOOK_SECRET_ENCRYPTION_KEY` rotation is a
4-step, operator-driven procedure; do not skip the verification step.

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
