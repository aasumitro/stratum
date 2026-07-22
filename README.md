# Stratum

> [!NOTE]
> This is a lightweight, personal edition of the original Stratum, maintained independently and kept separate from that codebase. It's published for personal use, not as a supported general-purpose commercial product, so it may not fit your business requirements as-is. If you need something tailored, fork this repo and customize it for your own use case rather than expecting drop-in fit.

A working B2B SaaS reference stack — multi-tenancy, billing, notifications, auth, a full web UI, and a desktop admin (Studio), wired together end-to-end. Fork it and adapt the pieces you need rather than dropping it in as-is.

## What's Included

- **Multi-tenant organizations** — memberships, roles (owner / admin / member), RBAC, invitations, invite codes, suspension
- **Auth** — JWT/JWKS validation (Supabase), no passwords to manage
- **Billing** — subscriptions, free trial, multi-currency (USD / IDR), proration, invoices with tax, PDF export, Stripe + Xendit payment links, webhooks, auto-renewal, usage metering, plan feature gates, dunning flow
- **File storage** — avatar upload, organization logo, organization file CRUD (upload / download / delete) via Supabase Storage; usage metered against billing plan
- **Notifications** — in-app messages + SMTP email (bilingual EN / ID templates), per-channel preferences, dunning and lifecycle emails
- **GDPR / PDPC** — async account deletion and data export with task tracking
- **Audit log** — auto-logged mutations and auth failures, configurable retention, CSV export
- **Observability** — OpenTelemetry traces, metrics, and logs
- **Web UI** — full React frontend

## Modules

| Module       | Path                | Description                                                         |
|--------------|---------------------|---------------------------------------------------------------------|
| Studio       | `ui/studio/`        | Wails v3 desktop admin - secure remote/local management             |
| API & Worker | `cmd/`, `internal/` | Go backend — Gin, PostgreSQL, RabbitMQ, Redis                       |
| Web UI       | `ui/app/`           | React 19 frontend — TanStack Router/Query, shadcn/ui, Supabase Auth |
| Mobile       | `ui/mobile/`        | Out of scope —  not part of the template                            |
| Marketing    | `ui/www/`           | Out of scope — not part of the template                             |

## Quick Start

```bash
# getting started
make help           #  check available command

# Backend
cp .env.example .env
make infra-up        # PostgreSQL 18, RabbitMQ 4, Redis 8
make migrate-up
make run-api         # :8000
make run-worker

# Web UI
cd ui/app
cp .env.example .env
npm install
npm run dev          # :3000

# Desktop admin (Stratum Studio)
cd ui/studio
task dev             # Wails v3 hot-reload (requires Go 1.25+ and wails3 CLI)
```

## Documentation

Details live in [`docs/`](docs/README.md):

- `docs/01-architecture.md` — module system, request flow, event bus
- `docs/02-getting-started.md` — setup, environment variables, make targets
- `docs/03-modules.md` — all modules, routes, events, contracts
- `docs/04-billing.md` — subscription lifecycle, renewal flow, payments, usage
- `docs/05-authentication.md` — JWT/JWKS, middleware, authorization
- `docs/06-database.md` — schema design, migrations, raw SQL patterns
- `docs/07-messaging.md` — RabbitMQ events, delayed messages, retry/DLQ
- `docs/08-api-reference.md` — every endpoint with request/response examples
- `docs/09-testing.md` — test architecture, helpers, coverage
- `docs/10-web-ui.md` — React frontend structure, routing, state, conventions
- `docs/11-file-storage.md` — avatar, logo, and organization file upload/download
- `docs/12-operations.md` — deployment topology, backups, secrets, RTO/RPO, health endpoints
- `docs/13-studio.md` — Stratum Studio: what it is, how it connects, features

See also: [`ui/app/README.md`](ui/app/README.md) and [`ui/studio/README.md`](ui/studio/README.md) — short pointers into the docs above.

## Changelog

See [CHANGELOG](CHANGELOG.md).

## License

See [LICENSE](LICENSE).
