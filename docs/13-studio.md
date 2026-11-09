# 13 · Stratum Studio (Desktop Admin)

Stratum Studio is a standalone **Wails v3 desktop app** operators use to manage many deployed Stratum projects at once — support, monitoring, and operational tasks that should never be exposed through the tenant-facing API. It lives in `ui/studio/` and has its own Go module.

## How it connects
Studio talks **directly** to each managed project's PostgreSQL, RabbitMQ, and Redis using operator-supplied DSNs — no API, no JWT, no tenant scoping. Because it works across tenants, the PostgreSQL DSN must bypass row-level security: `billing`, `organization` and `notification` run under `FORCE ROW LEVEL SECURITY`, and the RLS-constrained `stratum_app` role would see none of their rows. The only HTTP call it ever makes is `GET <project_url>/health` for uptime monitoring. This is a deliberate choice: it avoids building and securing a second API surface for internal operators. Studio is never exposed to a tenant.

## Operator roles
Each person connects with their own Postgres login role, a member of the `stratum_operator` group. That login is what the audit trail attributes changes to: a database trigger records every write as `operator:<role>` with the old and new row (minus invite codes, checkout URLs, owner identifiers and any person a row is aimed at), and broadcasts and queue actions are recorded explicitly before they run. If the record cannot be written, the action does not run. That row records the attempt: a queue action that then fails adds a `studio.dlq.requeue.failed` or `studio.dlq.purge.failed` row with the reason, and a broadcast that reaches only some recipients adds `studio.notification.broadcast.failed`. A project's database and broker DSNs must belong to the same deployment, because queue actions are recorded in that database.

Any other connection, such as the schema owner, can still read everything. So can a login in the group created without `BYPASSRLS`, as far as row-level security lets it, and it is refused too. Studio shows a persistent banner and refuses every change with `this connection is not an operator role; changes would not be audited`. A connection failure, such as an outage or an expired password, is refused too, but reported as itself. Local dev has a ready-made login: `postgres://stratum_operator_dev:stratum_operator_dev@localhost:5432/stratum?sslmode=disable`. Provisioning, the quarterly review and revocation are in [12-operations](12-operations.md). RabbitMQ and Redis credentials stay shared; queue actions are attributed through the database record, not by the broker.

## Stack
Wails v3 with a Go 1.26+ backend and a React 19 / TypeScript / Vite frontend (Tailwind v4 + shadcn/ui, TanStack Router). Its own project registry is stored locally in SQLite (`modernc.org/sqlite`, pure Go). Frontend types come from Wails-generated bindings, **not** from `ui/app`.

> Two cautions if you touch the code: don't move Studio into `cmd/` (its Wails dependency chain is incompatible with the main module), and read https://v3.wails.io/ first — Wails 3 differs significantly from v2.

## What it does
- **Project registry** — add/edit/delete managed projects, with inline DSN connection tests and color accents.
- **Cross-project overview** — home cards showing health, active organizations, subscriptions, and MRR per project at a glance.
- **Dashboard** — per-project metrics: organizations, members, subscriptions by status, MRR/ARR, storage (always zero since organization-level file storage was removed 2026-08-05 — the stat card itself wasn't), top plans.
- **Queue monitor** — browse all queues or dead-letter only; inspect messages, requeue (FIFO), purge.
- **Reference data** — full CRUD for countries and currencies, with delete guards (you can't delete a currency an active subscription uses).
- **Catalog management** — CRUD for plans, features, coupons, and add-ons, plus entitlement and coupon-target management, with delete guards.
- **Monitoring** — polls each project's readiness and runtime-stats endpoints on a configurable interval, with history and a sidebar status dot.
- **Support** — user search and detail (including login history), subscription overrides (extend trial and change plan, both handed to the app's billing worker, which moves the trial's end with its period and reschedules renewal, or changes the plan through the owner's own path so pending invoices are repriced; activate a trialing or past_due subscription), invoice actions (mark paid — applied by the app's billing worker like a real payment, so the subscription renews or reactivates; void — also closes the invoice's open checkouts at the provider and gives back the coupon application its discount used), and an at-risk invoices tab. That tab also lists payments to review: money a provider took that settled nothing (a payment on an invoice already void, failed or paid, including one an operator marked paid). Void also releases an add-on increase gated on the voided invoice. Refund each at the provider, then mark it refunded; Studio records that, it does not move money.
- **Broadcast** — send in-app notifications to all users, one organization, or one user, with a recipient-count preview and send history.
- **Organization ops** — a filterable table with suspend/unsuspend (reason required).
- **Audit log** — cross-organization, filterable, paginated, CSV export.
- **Operator log** — a local SQLite trail of the operator's support, organization and broadcast actions.
- **Watchlist** — trials ending soon, past-due, renewals due, and cancellations, with a sidebar count badge.

Each project's `audit.events` is the system of record for operator changes; the local log is a convenience on one machine. Global niceties: dark/light mode following the OS, a ⌘K command palette, and ⌘N to add a project.

## Running it
From `ui/studio/`: `task dev` (hot reload, regenerates bindings), `task build` (release), `task package` (signed/packaged). Studio doesn't need the API running — only the `/health` monitor calls it.

`go test ./app -run TestMutatingMethodsRequireOperator` fails when a mutating method skips the operator check. `TestOperatorSmoke` drives every mutating method against a real database and broker, as the owner and as an operator. It writes fixtures, so it runs only when `STUDIO_SMOKE_OWNER_URL`, `STUDIO_SMOKE_OPERATOR_URL` and `STUDIO_SMOKE_MQ_URL` are set; point them at a throwaway database.

## Target-database name gotchas
Because Studio queries target databases directly, a few table/column names matter: organizations are `organization.organizations` (not `tenants`), members are `organization.memberships`, storage reads `billing.usage.value`, and plans join via `billing.subscriptions.plan` → `billing.plans` (plans moved out of `ref` in the catalog rework).

## Deliberately not built
Real-time log streaming, Supabase user create/delete, organization deletion from Studio, billing reversals/refunds, multi-window layout, user impersonation, and an "Early Access" invite-code manager / read-only invitations view are all out of scope — each is either too destructive, a materially higher trust level than holding a DB DSN, or (for Early Access) irrelevant to how Studio is distributed: it's a private internal tool built and run locally, shared by handing over the binary or letting a teammate build from source, never released publicly or through a marketplace. For the same reason, macOS code signing/notarization is also skipped — Studio mostly runs on Linux, and the rare Mac use builds from source locally, which never triggers Gatekeeper's unsigned-app warning.
