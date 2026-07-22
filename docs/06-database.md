# 06 · Database

Stratum uses PostgreSQL 18 with raw SQL through `pgx/v5` — no ORM. The schema is organized to reinforce module boundaries.

## One schema per module

Six schemas, each owned exclusively by one module: `organization`, `account`, `billing`, `notification`, `ref`, and `audit`. (Supabase manages a seventh, `auth`, outside this database.)

The defining rule: **no cross-schema foreign keys.** When one module's table needs to reference another's row, it stores a plain UUID column and validates it in application code — never a database constraint spanning schemas. This keeps each module's migrations and deployments independent. Every table that refers to a person stores `auth_sub` (the Supabase user ID); no passwords, tokens, or session data are ever duplicated locally.

## No ORM

All queries are hand-written SQL — a deliberate choice: explicit queries, no hidden N+1s, and the database schema as the contract. Handlers stay thin; SQL lives in each module's repository layer.

## Migrations

Migrations live in `db/migrations/` as numbered up/down pairs, one per schema:

| # | Schema | Contents |
|---|---|---|
| 000001 | `ref` | currencies, countries + seed data |
| 000002 | `organization` | organizations, memberships, invitations, webhooks, files, folders |
| 000003 | `account` | users, tasks, login events |
| 000004 | `billing` | subscriptions, invoices, payments, usage, the catalog + row-level security |
| 000005 | `notification` | messages, preferences |
| 000006 | `audit` | events (append-only) |

During the current development phase, changes are edited **in place** into the existing schema file (which requires fresh infra after a rewrite); genuinely new schemas would start at `000007`. Apply with `make migrate-up`; create a new pair with `make migrate-create name=<snake_case>`. A single schema can be extracted with `pg_dump --schema=<schema>`.

## The billing catalog

The most involved part of the schema is the billing catalog, which replaced an older static three-row plan seed. `billing.plans` is a real foreign-key target for subscriptions; `billing.features` describes what a plan grants (by type — metered, boolean, static, or config); join tables connect plans and add-ons to features with limit values; and coupon tables handle discounts and their targeting. Coupon cadence and discount math are computed in Go (as pure, testable functions), not in SQL. See `04-billing.md` for how these tables drive behavior.

## Reference seed

Countries are seeded as the full ISO 3166-1 list with only Indonesia active by default, so activating a new market is a toggle in Studio rather than a hand-typed row. Currencies are a curated list (the most globally-used plus all ASEAN/SEA currencies) with only USD and IDR active. Countries whose real currency isn't in that set carry a `USD` placeholder to satisfy the not-null reference — correct it in Studio when you actually activate that market.

## A couple of gotchas
- `audit.events.id` is a random UUID (not time-ordered), so the per-organization audit list paginates by `id`, while the personal audit list orders by `created_at`.
- `notification.messages` names its columns counter-intuitively: `kind` is the delivery channel (`"in_app"`) and `channel` is the event type (`"invoice_created"`).
