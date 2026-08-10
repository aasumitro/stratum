# 13 · Stratum Studio (Desktop Admin)

Stratum Studio is a standalone **Wails v3 desktop app** operators use to manage many deployed Stratum projects at once — support, monitoring, and operational tasks that should never be exposed through the tenant-facing API. It lives in `ui/studio/` and has its own Go module.

## How it connects
Studio talks **directly** to each managed project's PostgreSQL, RabbitMQ, and Redis using operator-supplied DSNs — no API, no JWT, no tenant scoping. The only HTTP call it ever makes is `GET <project_url>/health` for uptime monitoring. This is a deliberate choice: it avoids building and securing a second API surface for internal operators. Studio is never exposed to a tenant.

## Stack
Wails v3 with a Go 1.25+ backend and a React 19 / TypeScript / Vite frontend (Tailwind v4 + shadcn/ui, TanStack Router). Its own project registry is stored locally in SQLite (`modernc.org/sqlite`, pure Go). Frontend types come from Wails-generated bindings, **not** from `ui/app`.

> Two cautions if you touch the code: don't move Studio into `cmd/` (its Wails dependency chain is incompatible with the main module), and read https://v3.wails.io/ first — Wails 3 differs significantly from v2.

## What it does
- **Project registry** — add/edit/delete managed projects, with inline DSN connection tests and color accents.
- **Cross-project overview** — home cards showing health, active organizations, subscriptions, and MRR per project at a glance.
- **Dashboard** — per-project metrics: organizations, members, subscriptions by status, MRR/ARR, storage (always zero since organization-level file storage was removed 2026-08-05 — the stat card itself wasn't), top plans.
- **Queue monitor** — browse all queues or dead-letter only; inspect messages, requeue (FIFO), purge.
- **Reference data** — full CRUD for countries and currencies, with delete guards (you can't delete a currency an active subscription uses).
- **Catalog management** — CRUD for plans, features, coupons, and add-ons, plus entitlement and coupon-target management, with delete guards.
- **Monitoring** — polls each project's readiness and runtime-stats endpoints on a configurable interval, with history and a sidebar status dot.
- **Support** — user search and detail (including login history), subscription overrides (extend trial, activate, change plan), invoice actions (mark paid, void), and an at-risk invoices tab.
- **Broadcast** — send in-app notifications to all users, one organization, or one user, with a recipient-count preview and send history.
- **Organization ops** — a filterable table with suspend/unsuspend (reason required).
- **Audit log** — cross-organization, filterable, paginated, CSV export.
- **Operator log** — a local SQLite trail of every write action an operator performs.
- **Watchlist** — trials ending soon, past-due, renewals due, and cancellations, with a sidebar count badge.

Every write action is logged locally, independent of the target project's own audit log. Global niceties: dark/light mode following the OS, a ⌘K command palette, and ⌘N to add a project.

## Running it
From `ui/studio/`: `task dev` (hot reload, regenerates bindings), `task build` (release), `task package` (signed/packaged). Studio doesn't need the API running — only the `/health` monitor calls it.

## Target-database name gotchas
Because Studio queries target databases directly, a few table/column names matter: organizations are `organization.organizations` (not `tenants`), members are `organization.memberships`, storage reads `billing.usage.value`, and plans join via `billing.subscriptions.plan` → `billing.plans` (plans moved out of `ref` in the catalog rework).

## Deliberately not built
Real-time log streaming, Supabase user create/delete, organization deletion from Studio, billing reversals/refunds, multi-window layout, user impersonation, and an "Early Access" invite-code manager / read-only invitations view are all out of scope — each is either too destructive, a materially higher trust level than holding a DB DSN, or (for Early Access) irrelevant to how Studio is distributed: it's a private internal tool built and run locally, shared by handing over the binary or letting a teammate build from source, never released publicly or through a marketplace. For the same reason, macOS code signing/notarization is also skipped — Studio mostly runs on Linux, and the rare Mac use builds from source locally, which never triggers Gatekeeper's unsigned-app warning.
