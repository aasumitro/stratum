# 06 · Database

Stratum uses PostgreSQL 18 with raw SQL through `pgx/v5` — no ORM. The schema is organized to reinforce module boundaries.

## One schema per module

Six schemas, each owned exclusively by one module: `organization`, `account`, `billing`, `notification`, `ref`, and `audit`. (Supabase manages a seventh, `auth`, outside this database.)

The defining rule: **no cross-schema foreign keys.** When one module's table needs to reference another's row, it stores a plain UUID column and validates it in application code — never a database constraint spanning schemas. This keeps each module's migrations and deployments independent. Every table that refers to a person stores `auth_sub` (the Supabase user ID); no passwords, tokens, or session data are ever duplicated locally.

## No ORM

All queries are hand-written SQL — a deliberate choice: explicit queries, no hidden N+1s, and the database schema as the contract. Handlers stay thin; SQL lives in each module's repository layer.

## Migrations

Migrations live in `db/migrations/` as numbered up/down pairs: one per schema through `000007`, then cross-cutting grants, policies and columns:

| # | Schema | Contents |
|---|---|---|
| 000001 | `ref` | currencies, countries + seed data |
| 000002 | `organization` | organizations, memberships, invitations, webhooks |
| 000003 | `account` | users, tasks, login events |
| 000004 | `billing` | subscriptions, invoices, payments, usage, the catalog + row-level security |
| 000005 | `notification` | messages, preferences |
| 000006 | `audit` | events (append-only) |
| 000007 | `messaging` | transactional outbox (`outbox` table) |
| 000008 | `-` | runtime + webhook roles and grant privileges (`stratum_app`, `stratum_worker`, `stratum_webhook`) |
| 000009 | `-` | grant privileges for the unscoped API role (`stratum_app_unscoped`): the `organization` schema + `messaging.outbox` |
| 000010 | `organization`, `notification` | row-level security (`FORCE ROW LEVEL SECURITY`) + tenant/caller policies |
| 000011 | `audit` | makes `audit.events` append-only for runtime roles; grants for the `stratum_operator` group; a trigger on 14 tables attributing operator changes; retention, anonymization and operator-action functions |
| 000012 | `billing`, `audit` | operator column grants on `billing.coupon_redemptions.applied_count` and `billing.payments.status`, with the attribution trigger on both; refuses `operator:` audit rows from any role without the table owner's privileges; anonymization also clears IP and user agent |
| 000013 | `billing` | `payments.unapplied` marks a payment that settled nothing (backfilled); operator column grant on `subscription_addons(pending_quantity, pending_invoice_id)` with the attribution trigger |
| 000014 | `billing` | `subscriptions.scheduled_effective_at`, the boundary a scheduled downgrade or add-on decrease applies at (backfilled from `period_end`) |
| 000015 | `billing` | `subscriptions.schedule_resend_count` and `schedule_resend_at`: the hourly reconcile's backoff for re-sending a due schedule's apply (doubling, at most daily; reset when the schedule changes) |

`000011`–`000015` change released schemas as new pairs, so a 0.5.0 database upgrades with `make migrate-up` once the `stratum_operator` role exists ([12-operations](12-operations.md)). Earlier releases edited schema files **in place**, which needed fresh infra (0.5.0's upgrade notes in `CHANGELOG.md`). The next free number is `000016`. Apply with `make migrate-up`; create a new pair with `make migrate-create name=<snake_case>`. A single schema can be extracted with `pg_dump --schema=<schema>`.

## The billing catalog

The most involved part of the schema is the billing catalog, which replaced an older static three-row plan seed. `billing.plans` is a real foreign-key target for subscriptions; `billing.features` describes what a plan grants (by type — metered, boolean, static, or config); join tables connect plans and add-ons to features with limit values; and coupon tables handle discounts and their targeting. Coupon cadence and discount math are computed in Go (as pure, testable functions), not in SQL. See `04-billing.md` for how these tables drive behavior.

## Reference seed

Countries are seeded as the full ISO 3166-1 list with only Indonesia active by default, so activating a new market is a toggle in Studio rather than a hand-typed row. Currencies are a curated list (the most globally-used plus all ASEAN/SEA currencies) with only USD and IDR active. Countries whose real currency isn't in that set carry a `USD` placeholder to satisfy the not-null reference — correct it in Studio when you actually activate that market.

## A couple of gotchas
- `audit.events.id` is a random UUID (not time-ordered), so the per-organization audit list paginates by `id`, while the personal audit list orders by `created_at`.
- `notification.messages` names its columns counter-intuitively: `kind` is the delivery channel (`"in_app"`) and `channel` is the event type (`"invoice_created"`).
