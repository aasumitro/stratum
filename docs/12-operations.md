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

## No scheduler
Stratum has no cron. Anything that looks periodic is either a one-shot RabbitMQ delayed message (dunning, renewal reminders) or lazy/opportunistic work (webhook health computed on delivery). This is a deliberate simplification — adding a scheduler for a single sweep wasn't judged worth the operational surface.
