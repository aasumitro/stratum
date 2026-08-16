# 09 · Testing

The backend has a three-layer test strategy that trades off speed against fidelity. Web and Studio verify with type-check, lint, and build, plus a Playwright e2e suite for the web app's auth and onboarding flows.

## The three layers

| Layer | File | Needs infra? | Covers |
|---|---|---|---|
| Pure | `pure_test.go` | No | Unexported helpers — math, normalization, signature verification |
| Handler | `handler_test.go` | No | HTTP binding, validation, RBAC (nil service + panic-recover) |
| Integration | `integration_test.go` | Postgres (+ RabbitMQ) | Full request → handler → service → DB round trips and DB constraints |

Anything unit-testable without a database is written as a **pure function** on purpose — coupon cadence, proration, usage-threshold crossing, and signature verification all test without infra.

## Helpers

- `httpserver.JSONTestRequest(method, path, body)` builds a request.
- `messaging.NoopPublisher{}` stands in for RabbitMQ.
- Each module's `export_test.go` exposes test engines (`NewHandlerEngine`, `NewModuleForTest`, `NewModuleEngine`, …).
- Integration tests skip when `TEST_DATABASE_URL` is unset, use fixed UUIDv7 data for isolation, and clean up with `t.Cleanup`.
- **`TEST_DATABASE_URL` points at `stratum_test`, a test-only role** (`POSTGRES_TEST_URL` in `.env.example`; created by `deploy/postgres-init/02-test-role.sql`) — not the migration-owner superuser. It holds `BYPASSRLS` directly (needed to seed `billing.subscriptions`/`invoices`/`payments`/`payment_links`, which carry `FORCE ROW LEVEL SECURITY`, directly) and is a member of `stratum`, the schema owner — membership, not a pile of per-privilege `GRANT`s, because several billing integration tests create a throwaway trigger function to inject a mid-transaction failure and then `DROP` it in `t.Cleanup`, and `DROP TRIGGER`/`DROP FUNCTION` check table/object *ownership*, not `CREATE`/`TRIGGER` privilege — a role merely granted those privileges can create the trigger but not drop it, leaving it attached to break every later test on that table. `stratum_test` is never provisioned outside local dev/CI and never appears in `docs/12-operations.md`'s production runbook — it exists purely so the suite doesn't need a superuser.

## Running

```bash
make test               # unit + handler, -race -count=1
make test-integration   # needs TEST_DATABASE_URL
```

Web's e2e suite runs separately, from `ui/app`:

```bash
npm run test:e2e   # Playwright — auth + onboarding, needs infra + API running
```

It logs in with the owner-role test account (`TEST_ACCOUNT_OWNER_EMAIL`/`PASSWORD`, repo-root `.env.local`) against real Supabase and the real backend, then intercepts the profile/organization lookups so the onboarding wizard's steps render deterministically without touching that account's actual state. `TEST_ACCOUNT_ADMIN_EMAIL`/`PASSWORD` and `TEST_ACCOUNT_MEMBER_EMAIL`/`PASSWORD` (same org, different roles) also live in `.env.local` for manual cross-role checks — the automated e2e suite only uses the owner account.

The full pre-merge sequence (what CI runs): `go vet ./...` → `gofmt -w .` → `golangci-lint run ./...` → `govulncheck ./...` → `go test ./... -race -count=1 -timeout 120s`.

## Coverage

Around 625 test runs, ~66% overall coverage (`make test-cover`). Highest where it matters most — middleware (~83%), reference/notification (~80%), audit (~84%), platform/db (~88%); billing ~75%, organization ~65%, account ~62%. Some packages are intentionally 0%: the live-JWKS auth middleware, the messaging broker layer, OpenTelemetry, the `cmd/*` entrypoints, and `internal/app` wiring — all thin or infra-bound.

## A lesson worth keeping

Running the integration suite against real Postgres/RabbitMQ/Redis has repeatedly surfaced bugs that the pure and handler layers can't — a nil-pointer path, a positional-parameter desync, a middleware gate that made a feature irreversible. Run the integration suite before calling feature work done; don't rely on unit tests alone.

## Conventions
Every bug fix gets a regression test. New business logic gets unit tests. System boundaries get integration tests. Avoid brittle tests that assert on incidental detail.
