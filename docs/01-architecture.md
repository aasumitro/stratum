# 01 · Architecture

Stratum is a **modular monolith**: a single Go codebase organized into independent modules that communicate through narrow, explicit seams. It ships as two binaries and is consumed by a web app and a desktop operator console.

## The big picture

```
        Supabase Auth (JWT / JWKS)
               │
              Web ───────────► API server (Gin) ───┐ publishes events
        (React / Vite)              │              │
                          ┌─────────┼─────────┐    ▼ RabbitMQ
                          ▼         ▼         ▼    Worker (Go)
                    Organization Billing Notification │
                          │         │         │       ▼
                          ▼         ▼         ▼   Stripe / Xendit webhooks
                        PostgreSQL (6 schemas)        │
                                              subscription/invoice state
                                                      │
                                          Notification + Audit

  Studio (desktop) ── direct DB / MQ / Redis ─► same Postgres / RabbitMQ / Redis
                                                (bypasses the API entirely)
```

## Why two binaries?

`cmd/api` answers HTTP requests and must stay fast. `cmd/worker` consumes background events — sending an email, checking a subscription's expiry, delivering a webhook — work that shouldn't add latency to a live request. They're built from the **same module code** and never call each other directly: they communicate only by publishing and consuming RabbitMQ messages.

## Modules and boundaries

The backend has five domain modules — `organization`, `account`, `billing`, `notification`, `reference` — each owning its own domain logic, database schema, HTTP handlers, Redis namespace, and message queues. Two rules keep them independent:

- **No cross-module imports.** A module never imports another. The only shared surface is `internal/contracts` — a small set of interfaces plus the event envelope.
- **No cross-schema foreign keys.** A reference to another module's data is a plain UUID column, validated in application code, never enforced by a database constraint. This keeps migrations and deployments decoupled.

Modules are wired together explicitly in `internal/app/api.go` (and `worker.go`) — each is constructed with a `New(...)` call, its dependencies resolved, and its routes or consumers registered. There is no magic `init()` wiring.

## How modules talk

- **Synchronously**, when a module needs an answer right now — e.g. billing asking the organization module whether an org is still active. This goes through a `contracts` interface resolved once at startup.
- **Asynchronously**, when a module needs to react to something without blocking — e.g. notification sending a welcome email after an organization is created. This goes over a RabbitMQ topic exchange; the worker's consumer handles it.

## The request path

Every API request passes through a middleware stack, in order: **auth** (validate the JWT against Supabase's JWKS), **organization** (resolve the active tenant), **RBAC** (owner/admin/member), **rate-limit** (per plan tier), **request-id**, **recovery**, **audit** (log every mutation automatically), **body-size**, and **CORS**. By the time a handler runs, identity, tenant, and permission are already established.

## The platform layer

Cross-cutting infrastructure lives in `internal/platform/` — one package per concern: `audit`, `cache` (Redis), `config`, `db` (pgx pool), `httpserver`, `mailer`, `messaging` (RabbitMQ), `otel`, `pdf`, `storage`. There is deliberately no `util/` or `helper/` grab-bag — every helper has a named home.

## Design principles

Prefer extending a module over adding one. Keep dependencies pointing inward. Hide implementation behind package boundaries. Favor explicit dependencies over implicit behavior. Introduce abstractions only when they reduce real duplication. When a tradeoff arises, prioritize correctness, then simplicity, then maintainability — optimize only with evidence.
