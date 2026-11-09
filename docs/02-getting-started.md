# 02 · Getting Started

This gets a full Stratum stack running locally: the API, the worker, the web app, and (optionally) the desktop console.

## Prerequisites
- Go 1.27+ (backend)
- Node 20+ and npm
- Podman (or Docker) for Postgres 18, RabbitMQ 4, Redis 8
- `migrate` CLI (or use the `make migrate-*` targets)
- For Studio: Go 1.26+ (stays on 1.26 until Wails v3 supports 1.27) and the `wails3` CLI, plus Task (`task`)
- A Supabase project (for auth)

## Backend

```bash
cp .env.example .env      # fill in the required values (see below)
make infra-up             # start Postgres 18, RabbitMQ 4, Redis 8 (containers)
make migrate-up           # apply all migrations
make run-api              # API server on :8000
make run-worker           # background worker (RabbitMQ consumers)
```

Storage buckets are created automatically on API startup. Confirm the stack is healthy with `curl localhost:8000/health/ready` (expect `200`) and check the worker log shows its consumers registered. In `development` mode, browse the interactive API docs at `localhost:8000/swagger/index.html` (Swagger UI, not mounted in production).

Other useful targets: `make build` (compiles `bin/api` + `bin/worker`), `make test` / `make test-integration`, `make lint` / `make fmt` / `make vet`, `make migrate-create name=<snake_case>`, `make swagger` (regenerates the OpenAPI spec after changing a handler's doc comments — see `08-api-reference.md`), `make infra-down` / `make infra-destroy`.

## Web app

```bash
cd ui/app
cp .env.example .env       # set VITE_SERVER_URL (default http://localhost:8000/api/v1)
npm install
npm run dev                # Vite dev server on :3000
```

Before finishing web work, run `npm run typecheck`, `npm run lint`, `npx prettier --check`, and `npm run build`.

## Desktop console (Stratum Studio)

```bash
cd ui/studio
task dev                   # Wails v3 hot reload (regenerates bindings)
```

Studio stores its own project registry in local SQLite and connects directly to each managed project's Postgres/RabbitMQ/Redis — it does not need the API running (except for the `/health` monitor).

## Environment variables

Everything is read once at startup from the environment (see `.env.example`). The essentials:

| Variable | Purpose |
|---|---|
| `POSTGRES_APP_URL` | Postgres connection for API (`stratum_app` role) (required) |
| `POSTGRES_WORKER_URL` | Postgres connection for Worker (`stratum_worker` role) (required) |
| `POSTGRES_WEBHOOK_URL` | Postgres connection for payment webhooks (`stratum_webhook` role) (required, both binaries) |
| `POSTGRES_UNSCOPED_URL` | Postgres connection for the API's organization paths with no tenant scope — authorization lookups, cross-org invite/join (`stratum_app_unscoped` role) (required, both binaries) |
| `POSTGRES_MAX_OPEN_CONNS` | Size of the main pool (default 20) |
| `POSTGRES_BACKGROUND_MAX_OPEN_CONNS` | Size of the API's background pool, which must stay below the main pool (default 5) |
| `POSTGRES_UNSCOPED_MAX_OPEN_CONNS` | Size of the API's unscoped pool, which must stay below the main pool (default 5) |
| `POSTGRES_MAX_IDLE_TIME` | How long an idle connection stays open (default 5m) |
| `POSTGRES_STATEMENT_TIMEOUT` | Per-statement timeout on every pool, 0 disables (default 30s) |
| `POSTGRES_URL` | Schema-owner connection for `make migrate-*` only; no binary reads it |
| `POSTGRES_TEST_URL` | `stratum_test` role for `make test-integration`; local dev and CI only |
| `POSTGRES_OPERATOR_URL` | `stratum_operator_dev` login for integration tests and local Studio; local dev and CI only |
| `REDIS_URL` | Redis connection (required) |
| `RABBITMQ_URL` | RabbitMQ connection (required) |
| `AUTH_JWKS_URL`, `AUTH_ISSUER` | JWKS endpoint + issuer for token validation (required) |
| `AUTH_AUDIENCE`, `AUTH_ADMIN_URL`, `AUTH_SERVICE_ROLE_KEY` | Supabase audience + Admin API (sessions, MFA sync) |
| `AUTH_ACCESS_TOKEN_MAX_TTL` | Max access-token lifetime (must match or exceed Supabase JWT expiry, default: 1h) |
| `SUPABASE_WEBHOOK_SECRET` | Shared header for the auth.users Database Webhook (email sync) |
| `STRIPE_API_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_SUCCESS_URL`, `STRIPE_CANCEL_URL` | Stripe payments (USD) |
| `XENDIT_API_KEY`, `XENDIT_CALLBACK_TOKEN`, `XENDIT_ALLOWED_CIDRS` | Xendit payments (IDR) |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM_NAME` | Email delivery |
| `WEBHOOK_SECRET_ENCRYPTION_KEY` | Passphrase encrypting customer webhook signing secrets (required) |
| `WEBHOOK_SECRET_ENCRYPTION_KEY_PREVIOUS`, `WEBHOOK_SECRET_ENCRYPTION_KEY_VERSION` | Set only while rotating that passphrase |
| `APP_ENV` | `development`, `staging` or `production`; outside development, boot enforces secrets, and HSTS and transport warnings turn on (default `development`) |
| `PORT`, `APP_URL` | API listen port and the web app's public URL |
| `SERVICE_NAME`, `SERVICE_VERSION` | Telemetry resource attributes |
| `LOG_LEVEL`, `LOG_FORMAT` | Logger settings (defaults `info`, `json`) |
| `TRUSTED_PROXIES` | CIDR ranges allowed to supply the client IP; set this behind any proxy or load balancer |
| `STATS_TOKEN` | Gates `GET /health/stats`; required outside development |
| `GEOIP_DB_PATH` | MaxMind Country database for billing country resolution; required outside development |
| `STORAGE_URL` | Supabase S3-compatible storage (optional — nil-safe if unset) |
| `OTEL_COLLECTOR_URL` | OpenTelemetry collector (optional — telemetry disabled if empty) |
| `CORS_ORIGINS` | Comma-separated allowed origins (empty = allow all, dev only) |
| `AUDIT_RETENTION_DAYS` | Audit log retention (default 400). Boot fails below 365, and the database enforces the same floor |
| `OUTBOX_RETENTION_DAYS` | How long published outbox rows are kept (default 30) |
| `LOGIN_EVENT_RETENTION_DAYS` | Login history retention (default 400) |
| `DATA_EXPORT_RETENTION_DAYS` | How long a completed GDPR export stays downloadable (default 7) |
| `NOTIFICATION_RETENTION_DAYS` | Notification retention (default 180) |
| `WEBHOOK_DELIVERY_RETENTION_DAYS` | Customer webhook delivery log retention (default 30) |
| `DELETED_ORGANIZATION_RETENTION_DAYS` | Grace period before a soft-deleted organization is permanently purged (default 30) |
| `WEBHOOK_EVENT_RETENTION_DAYS` | Provider webhook replay marker retention (default 90) |

Many integrations degrade gracefully when unconfigured — storage, SMTP, OpenTelemetry, and payment webhook verification all skip cleanly in local dev. Real credentials live in `.env.local` (gitignored) and are never committed or logged.

## Where to go next
- `03-modules.md` — what each backend module does
- `08-api-reference.md` — the endpoints
- `10-web-ui.md` — the frontend
- `12-operations.md` — running it in production
