# Stratum Documentation

Engineering documentation for Stratum — a production-ready multi-tenant B2B SaaS starter. New here? Start with `01-architecture.md`, then `02-getting-started.md`. Come back to the rest as you need them.

> These docs describe current system behavior and are kept in sync with the code.
> 
## Contents

| Doc                                         | What it covers                                            |
|---------------------------------------------|-----------------------------------------------------------|
| [01-architecture](01-architecture.md)       | The module system, request flow, and event bus            |
| [02-getting-started](02-getting-started.md) | Setup, environment variables, make targets                |
| [03-modules](03-modules.md)                 | Every backend module — responsibilities, routes, events   |
| [04-billing](04-billing.md)                 | Subscription lifecycle, catalog, renewal, payments, usage |
| [05-authentication](05-authentication.md)   | JWT/JWKS, MFA, middleware, authorization                  |
| [06-database](06-database.md)               | Schema design, migrations, raw-SQL patterns               |
| [07-messaging](07-messaging.md)             | RabbitMQ events, delayed messages, retry/DLQ              |
| [08-api-reference](08-api-reference.md)     | Endpoints with request/response shapes                    |
| [09-testing](09-testing.md)                 | Test architecture, helpers, coverage                      |
| [10-web-ui](10-web-ui.md)                   | React frontend structure, routing, state, conventions     |
| [11-file-storage](11-file-storage.md)       | Avatar and organization logo storage                       |
| [12-operations](12-operations.md)           | Deployment, backups, secrets, health, runbooks            |
| [13-studio](13-studio.md)                   | Stratum Studio — the Wails desktop operator console       |

[specs/](specs) contains the generated OpenAPI (Swagger) specification. 

## The one-paragraph tour

Stratum is a **Go modular monolith** compiled into two binaries — an **API server** and a **Worker** — that talk only over RabbitMQ. A **React web app** (`ui/app`) is its client, and a **Wails desktop console** (`ui/studio`) lets operators manage many deployed projects by connecting straight to their databases. Auth is delegated to Supabase (JWT via JWKS); everything else — organizations, billing, notifications, audit — is in-house. PostgreSQL uses one schema per module with no cross-schema foreign keys, and modules never import each other: the only seam is a small contracts layer.
