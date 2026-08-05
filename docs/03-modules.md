# 03 · Modules

The backend is five domain modules plus a platform layer. Each module owns its schema, handlers, and events, and exposes only what other modules need through `internal/contracts`.

## Dependency order

```
Authentication (Supabase) → Account → Organization → Billing → Payment Integration
                                          │
                                          ├─► Notification (event-driven, all modules)
                                          └─► Audit (middleware-driven, all mutations)
Reference depends on Billing (the catalog's schema owner) to serve its own plan/feature/addon routes — everything else it owns (countries, currencies, tax rates) is a true leaf.
```

## Account
Represents a human user, independent of any organization. Profile (name, avatar, arbitrary JSON preferences), async account deletion and GDPR export (via RabbitMQ, tracked as tasks), session/login history with revoke-all, and MFA status sync. The only identity reference stored is `auth_sub`.

## Organization
The tenant — a company/workspace that owns billing, membership, and most business data. Handles the organization profile (name, slug, country, logo, timezone, locale, IP allowlist), membership with a fixed `owner > admin > member` role model, invitations (by email or 8-character invite code, with bulk CSV import), ownership transfer, self-service suspend/unsuspend, and outbound customer webhooks. RBAC lives here, cached in Redis for 30 seconds.

## Billing
The subscription and license engine. A real catalog of plans, features, add-ons, and coupons drives entitlements; subscriptions move through `trialing → active → cancelled → expired` (with `active ⇄ past_due`). Supports plan changes with proration (downgrades below current usage resolve the overage — remove members — as part of the same request), cancel/resume, tiered-pricing extend (with an annual-cycle-conversion option), immediate trial activation, invoices with per-country tax and PDF rendering, usage metering with quota enforcement, and renewal/dunning reminders. See `04-billing.md`.

## Payment Integration
Deliberately minimal — collect money and tell Stratum it happened. Stripe (USD) and Xendit (IDR) generate hosted payment links; verified webhooks are the *only* thing that moves billing state. No card data is ever stored.

## Notification
In-house delivery across email (bilingual EN/ID templates), in-app, outbound customer webhook, and SSE real-time push. A per-user opt-out matrix (channel × event type) governs delivery, and emails use the recipient's own language preference. See `07-messaging.md` for the event wiring and `README`'s notification section.

## Reference
Static seed data read by everything: countries (full ISO 3166-1, only Indonesia active by default) and currencies (a curated set, only USD/IDR active). Activating a new market is a Studio toggle rather than a code change.

## Audit
Not a module you call — middleware records every non-GET request (and every 4xx/5xx, including anonymous auth failures) into an append-only log, with sensitive fields redacted. Two read surfaces exist: a per-organization admin log and a personal cross-organization log.

## The platform layer
`internal/platform/` holds the shared infrastructure — audit, cache, config, db, httpserver, mailer, messaging, otel, pdf, storage — one package per concern. Modules depend on the platform, never on each other.

## Routes at a glance
Organization: `/organizations/*` (members, invitations, webhooks, settings, suspend). Account: `/me/*`. Billing: `/organizations/:id/billing/*`. Notifications: `/me/notifications/*`. Reference: `/references/*`. Full detail in `08-api-reference.md`.
