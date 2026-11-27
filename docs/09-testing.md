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
- Fixture email addresses use the reserved `example.com` domain, never a real one, so a fixture that reaches a real send can't land in anyone's inbox.
- Tests of a role's privileges connect as that role through `POSTGRES_APP_URL`, `POSTGRES_WORKER_URL`, `POSTGRES_WEBHOOK_URL` and `POSTGRES_OPERATOR_URL`, with fixtures through `TEST_DATABASE_URL`. `TestQueueMessageCount` reads `RABBITMQ_URL`. `make test-integration` takes all of them from `.env` and points every Postgres URL at the test database (below).
- Retention sweep tests use cutoffs a century back, because a sweep touches every row past its cutoff and every package's tests share one database.
- **`TEST_DATABASE_URL` points at `stratum_test`, a test-only role** (`POSTGRES_TEST_URL` in `.env.example`; created by `deploy/postgres-init/02-test-role.sql`) — not the migration-owner superuser. It holds `BYPASSRLS` directly (needed to seed `billing.subscriptions`/`invoices`/`payments`/`payment_links`, which carry `FORCE ROW LEVEL SECURITY`, directly) and is a member of `stratum`, the schema owner — membership, not a pile of per-privilege `GRANT`s, because several billing integration tests create a throwaway trigger function to inject a mid-transaction failure and then `DROP` it in `t.Cleanup`, and `DROP TRIGGER`/`DROP FUNCTION` check table/object *ownership*, not `CREATE`/`TRIGGER` privilege — a role merely granted those privileges can create the trigger but not drop it, leaving it attached to break every later test on that table. `stratum_test` is never provisioned outside local dev/CI and never appears in `docs/12-operations.md`'s production runbook — it exists purely so the suite doesn't need a superuser.

## Running

```bash
make test               # unit + handler, -race -count=1, no database
make test-integration   # needs infra; runs against stratum_test, not the dev database
```

`make test-integration` (also `make test-cover` and `make pre-push`) first runs `make test-db`. It creates the `stratum_test` database in the local Postgres if it is missing and migrates it up. The suite then runs with every `POSTGRES_*_URL` and `TEST_DATABASE_URL` taken from `.env`, with the database swapped for `stratum_test`. This works for URLs of the `.env.example` form `…/stratum?…`. `make test-db` stops with an error if any of these URLs is not in that form, or if `TEST_DB` is `stratum`: such a URL would still reach the dev database. The database is created in the compose `postgres` service, so `.env` must point at that server, as it does by default. The roles are cluster-wide and the migrations grant them, so the test database needs no other setup.

`make test` runs the same packages with every `POSTGRES_*_URL` blanked, so its database tests skip and it never reaches a database.

So a local `cmd/worker` can keep running during the suite. It relays from the dev database and never sees the suite's outbox rows. The relay tests in turn never claim the dev data's due rows. Redis is still shared (`TEST_REDIS_URL=$(CACHE_URL)`). `cmd/worker` does not use Redis.

Use another database name with `make test-integration TEST_DB=<name>`. Drop the test database and run `make test-db` again in three cases:
- a migration failed half-way and left it dirty;
- `migrate` reports `no migration found for version N`, because the database was migrated on a branch with a newer migration;
- an already-applied migration was edited, so `migrate up` reports no change on a stale schema.

To drop it, run `podman exec stratum-postgres dropdb -U stratum stratum_test`, using your `TEST_DB` name if you override it.

The audit archive's tests run against a fake bucket. `make test-audit-archive` runs the ones that need a real S3 server: it starts the local one and proves that the server itself refuses to change a locked object, for its administrator too. It leaves a bucket of locked objects per run in that server's volume; `make audit-archive-down` stops the server, and removing its volume (`stratum-audit-archive-data`) starts it clean.

Web's unit tests run with `npm test` in `ui/app`, which pins `LC_ALL=en_US.UTF-8` and `TZ=UTC`, so date assertions hold on any machine. Studio's Go tests (`go test ./...` in `ui/studio`) use an in-memory keychain; the operator smoke test runs only when `STUDIO_SMOKE_OWNER_URL`, `STUDIO_SMOKE_OPERATOR_URL`, `STUDIO_SMOKE_MQ_URL` and `STUDIO_SMOKE_MQ_ADMIN_URL` point at a database and broker.

Web's e2e suite runs separately, from `ui/app`:

```bash
npm run test:e2e   # Playwright — auth + onboarding, needs infra + API running
```

It logs in with the owner-role test account (`TEST_ACCOUNT_OWNER_EMAIL`/`PASSWORD`, repo-root `.env.local`) against real Supabase and the real backend, then intercepts the profile/organization lookups so the onboarding wizard's steps render deterministically without touching that account's actual state. `TEST_ACCOUNT_ADMIN_EMAIL`/`PASSWORD` and `TEST_ACCOUNT_MEMBER_EMAIL`/`PASSWORD` (same org, different roles) also live in `.env.local` for manual cross-role checks — the automated e2e suite only uses the owner account.

Every test signs in for real, so back-to-back full runs can trip Supabase's sign-in rate limit: tests then time out on the login page, which shows "Request rate limit reached". Wait a few minutes before rerunning. Billing specs mock the org-scoped catalogs (`/billing/plans/catalog`, `/billing/addons/catalog`) the billing page actually reads, and every attached add-on carries its catalog `prices`, as the API returns them.

The full pre-merge sequence (what CI runs for the API): `go vet ./...` → `gofmt -w .` → `golangci-lint run ./...` → `govulncheck ./...` → `go test ./... -race -count=1 -timeout 120s`. CI also scans the git history for secrets (gitleaks) on every event, and when the images' inputs change builds the API and worker images, records an SPDX SBOM for each (syft) and scans them (Trivy, failing on a fixable HIGH/CRITICAL).

## Coverage

Around 625 test runs, ~66% overall coverage (`make test-cover`). Highest where it matters most — middleware (~83%), reference/notification (~80%), audit (~84%), platform/db (~88%); billing ~75%, organization ~65%, account ~62%. Some packages are intentionally 0%: the live-JWKS auth middleware, the messaging broker layer, OpenTelemetry, the `cmd/*` entrypoints, and `internal/app` wiring — all thin or infra-bound.

## A lesson worth keeping

Running the integration suite against real Postgres/RabbitMQ/Redis has repeatedly surfaced bugs that the pure and handler layers can't — a nil-pointer path, a positional-parameter desync, a middleware gate that made a feature irreversible. Run the integration suite before calling feature work done; don't rely on unit tests alone.

## Conventions
Every bug fix gets a regression test. New business logic gets unit tests. System boundaries get integration tests. Avoid brittle tests that assert on incidental detail.
