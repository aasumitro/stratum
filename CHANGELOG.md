# Changelog

All notable changes to Stratum are documented here.
Format based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
versioning follows [SemVer](https://semver.org/).

## [Unreleased]

## [0.4.0] - 2026-08-16

Four rounds of backend security audit remediation since 0.3.0, plus same-day follow-on fixes.
Round 1 was an external audit (16 findings); rounds 2–4 were independent re-audits of each prior
round's own new code (11, 6, and 3 findings).

### Added

- Transactional outbox (`messaging.outbox`): every domain event (organization created, GDPR
  export requested, invoice issued, etc.) now writes durably to the database in the same
  transaction as the state change it describes, instead of publishing directly to RabbitMQ and
  silently dropping the event on a broker outage or crash. A `cmd/worker` relay delivers
  unpublished rows on a 2-second poll
- `OUTBOX_RETENTION_DAYS` (optional, default `30`): hourly sweep deletes published
  `messaging.outbox` rows older than this many days; unpublished rows are never touched
- `cmd/api` and `cmd/worker` now connect to Postgres as separate, minimally-privileged roles
  (`stratum_app`/`stratum_worker`) instead of one shared owner role; new env vars
  `POSTGRES_APP_URL`/`POSTGRES_WORKER_URL`. Payment webhooks (`POST /webhooks/stripe`/`xendit`)
  run through a third, dedicated `stratum_webhook` role and its own connection pool, since those
  routes have no user session to scope row-level security by — new env var `POSTGRES_WEBHOOK_URL`
- Webhook signing secrets are now encrypted at rest (`WEBHOOK_SECRET_ENCRYPTION_KEY`, required
  outside development)
- `WEBHOOK_SECRET_ENCRYPTION_KEY_PREVIOUS`/`WEBHOOK_SECRET_ENCRYPTION_KEY_VERSION`
  (optional): support a zero-downtime `WEBHOOK_SECRET_ENCRYPTION_KEY` rotation — see
  "Rotating the webhook encryption key" in `docs/12-operations.md`
- `AUTH_ACCESS_TOKEN_MAX_TTL` (optional, default `1h`): the maximum lifetime a
  Supabase-issued access token can have in this deployment; used to size the session
  revocation epoch's TTL
- `POSTGRES_TEST_URL` (dev/CI only): a dedicated `stratum_test` role for the integration
  suite, replacing an accidental dependency on the CI Postgres image's superuser default —
  see `docs/09-testing.md`
- `/webhooks` (inbound Stripe/Xendit/Supabase callbacks) gained a 6000/min per-IP rate-limit
  backstop, closing the gap left when the IP-keyed limiter was removed from this route earlier

### Changed

- **Deployment order changed**: creating `stratum_app`/`stratum_worker`/`stratum_webhook` is no
  longer a side effect of `make migrate-up` — they must be provisioned first
  (`deploy/postgres-init/01-app-role.sql`, automatic in local dev and CI; manual elsewhere, see
  "Provisioning database roles" in `docs/12-operations.md`). Migrating without doing this now
  fails immediately and clearly on migration `000008`, instead of the migration creating the
  roles itself as it did before
- `billing`'s row-level security policies now carry `FORCE ROW LEVEL SECURITY` — owning a table no
  longer silently bypasses its own RLS policies
- `POSTGRES_URL` is replaced by `POSTGRES_APP_URL`/`POSTGRES_WORKER_URL`/`POSTGRES_WEBHOOK_URL`
  for the app's own runtime connections (still used as the migration-owner DSN); `WEBHOOK_SECRET_ENCRYPTION_KEY`
  and `STATS_TOKEN` are now required outside development. See "Upgrading an existing deployment"
  in `docs/12-operations.md` for the required order
- An exhausted `messaging.outbox` row's alerting is now two distinct conditions instead of one
  ("delivery delayed, nothing lost" vs. "abandoned, manual replay required") — see
  `docs/12-operations.md`
- Organization creation with a cart-selected coupon now fails provisioning (instead of
  silently dropping the discount) if the coupon is exhausted by someone else between
  submission and processing — see "Dead-letter backlog" in `docs/12-operations.md` for the
  operational tradeoff this introduces
- Percent-off coupon discounts now round to the nearest cent instead of truncating,
  correcting a systematic under-discount on every percent-off invoice line
- A background member-usage sync failure is now logged instead of silently dropped (the
  sync itself is unchanged — still fire-and-forget, still self-corrects on the next mutation)

### Fixed

- A subscription reactivated from `expired` (or resumed from `cancelled` past its trial window)
  kept a stale `trial_end`, so the next scheduled check could incorrectly re-expire a paying
  customer's subscription and suspend their organization
- Revoking an invitation ignored which organization it belonged to — any org admin/owner could
  delete any pending invitation in the system by ID, not just their own organization's
  (cross-tenant)
- Regenerating a payment link could expire another tenant's pending payment link before the
  ownership check ran (cross-tenant)
- The worker process's account module was wired with a stub storage client, so GDPR account
  deletion never actually removed the user's avatar while reporting that step as succeeded
- "Sign out everywhere" only revoked the *calling* session's access token — other devices'
  already-issued tokens stayed valid until natural expiry (up to `AUTH_ACCESS_TOKEN_MAX_TTL`)
  instead of being revoked immediately as documented
- `GET /me/invitations` returned live invitation tokens without the email-verification check
  every sibling route already enforced
- Payment webhooks were IP-rate-limited, producing spurious `429`s under any traffic burst or
  behind a proxy
- The rate-limit check ran before the organization-membership check, leaking a target
  organization's plan tier via response headers on an otherwise-`403` request
- The SSRF blocklist for outbound webhook URLs didn't cover every shared/special-use address range
- Organization invite codes were missing from the audit-log redaction list
- Event envelope IDs were generated as UUIDv4 while documented (and relied on for ordering) as
  UUIDv7
- A repository function running a row-lock (`FOR UPDATE`) query now fails loudly if called outside
  an active transaction, instead of silently running with no isolation guarantee
- Two fail-open dependencies gating production-critical controls (session revocation, seat-limit
  and usage tracking) now refuse to start if left unwired, instead of failing open silently
- Removing a member, changing a member's role, and deleting a webhook endpoint now return `404`
  for a cross-tenant sub-resource ID instead of a silent `204` no-op; retrying one or all failed
  webhook deliveries now returns `404` for the same case instead of `500`. No data was ever leaked
  in either case — API consistency only
- Background/batch database writes (audit-log flush, last-seen-at updates) now use a separate
  connection pool from the request-serving path, closing a self-deadlock class under concurrent
  load — including the specific case where the billing catalog's own helpers could deadlock the
  whole request pool under concurrency
- Three silent-failure regressions introduced by the role/RLS split above were caught and fixed
  the same round: payment-webhook confirmation, background usage recording, and billing
  feature/limit checks all silently no-op'd under the new restricted roles instead of erroring
- An outbox row that can never publish (a malformed payload, a permanently-unroutable key) no
  longer retries forever and stalls the relay — capped at 20 attempts, with backoff between
  retries
- The AMQP publisher now self-recovers from a broker-initiated channel close (a capped-backoff
  retry loop) instead of requiring a process restart, and no longer leaks a channel and a
  goroutine when that recovery races a connection-level reconnect
- A secret-placeholder rejection (refusing an obviously-unset value like `CHANGE_ME...` outside
  development) now runs in `cmd/worker` too — previously only `cmd/api` checked it, even though
  `cmd/worker` is the binary that actually decrypts webhook secrets
- Migration `000008` no longer hardcodes the `stratum` database/owner names; on a deployment
  using a different database name, the previous version failed silently — grants landed on
  whichever database happened to be named `stratum`, only surfacing once the standard
  `REVOKE CONNECT ... FROM PUBLIC` hardening step was applied
- `composeAndInsertActivationInvoice` (billing) no longer silently ignores a database error
  while checking for a pending invoice, which could have risked creating a duplicate invoice
  on a transient failure

### Removed

- Empty, unused `deploy/docker-compose.staging.yml` placeholder

## [0.3.0] - 2026-08-10

### Added

- Server-resolved billing currency: `country_code` is now resolved from the caller's real IP
  (MaxMind GeoLite2) at organization creation and plan/addon catalog lookups instead of being a
  client-supplied field, closing a price-arbitrage hole
- In-app notifications now render in the viewer's current UI language, including history read
  later — previously frozen in English at write time
- Backend error codes now drive frontend translation: every error the API can throw has an
  EN/ID translation, enforced by a coverage test; validation field errors carry a translatable
  `{code, param}` pair instead of a prebuilt English sentence
- Zod-backed schema validation adopted across frontend forms
- Stripe/Xendit checkout pages show a real itemized invoice breakdown instead of a bare
  "Invoice `<uuid>`" line
- Plan History now distinguishes scheduled vs. applied vs. undone changes, records cycle-only
  switches, and covers the full addon lifecycle (attach/increase/decrease/undo) — previously
  addon changes never appeared in history at all
- Org-scoped plan/addon catalog pricing for post-creation pickers, so a traveling owner sees
  their organization's own currency instead of a GeoIP-resolved one

### Changed

- Subscription extensions now bill attached addons for the extended window instead of letting
  them ride free; addon-increase proration now accounts for the subscription's real (possibly
  multi-cycle) period instead of assuming a single billing cycle
- 9 confirmatory/non-time-sensitive notification types (welcome, trial started, invoice created,
  subscription activated/cancelled/resumed, role changed, ownership transferred, invitation
  requested) are now in-app-only, reducing email noise; time-sensitive/financial/security
  notifications are unchanged
- Organization file/folder storage removed entirely (upload/download/folders/trash/quota and the
  extra-storage add-on); avatars, organization logos, and invoice PDFs are unaffected
- Member seat-limit checks are now atomic (row-locked) across add/invite-accept/join-by-code,
  closing a race that could over-fill a seat-limited organization

### Fixed

- Organization invitation resend endpoint (IDOR): now verifies the caller's verified email
  against the invitation's target instead of trusting any authenticated caller
- Accepting an invitation to a suspended organization no longer bypasses the suspension gate
- Non-atomic billing webhook handling, a coupon double-redemption race, and an orphaned invoice
  on partial failure fixed with proper transaction boundaries, including two worker-invoked
  paths (subscription expiry, cancel-on-deletion) that previously had no audit trail on failure
- SSRF, CORS, webhook-secret, and rate-limiting hardening; auth redirect and CSP hardening
- SSE concurrent-stream slot no longer drops on a connection held open past 10 minutes (counter
  TTL is now refreshed for the life of the stream)
- Notification "Load more" pagination no longer breaks depending on cursor vs. page usage
- Removing an already-removed organization member no longer publishes a phantom removal event

## [0.2.0] - 2026-07-25

### Added

- Self-service subscription plan changes: upgrade, downgrade (with overage
  handling), cancel, and extend flows, each with a dedicated wizard/dialog
  and proration preview
- Extend pricing: 12+ month extensions bill yearly-price blocks plus a
  monthly-price remainder, cap unchanged at 2 years
- `kind: "extension"` on invoices to distinguish extension invoices from
  normal subscription and renewal invoices
- Centralized permission matrix as the single source of truth for
  role-based permissions
- Shared `RouteTabs` component for Settings, Billing, and Members
  sub-navigation, with horizontal scrolling for overflow

### Changed

- Defer subscription `period_end` updates until the extension invoice is
  confirmed paid via webhook
- Per-subscription invoice-number uniqueness (`UNIQUE(subscription_id,
  invoice_number)`) replacing the previous global constraint
- `usePermissions()` now reads from the shared permission matrix;
  navigation hides inaccessible items instead of rendering them disabled
- Import Members and Invite Member consolidated into a single dropdown
  action

### Fixed

- Idempotent paid-webhook handling; roll back the full transaction if
  applying payment fails instead of leaving invoices partially updated
- Stale delayed renewal reminder/auto-invoice jobs are ignored after a
  subscription period changes
- Duplicate extension requests rejected while one is already pending;
  expired subscriptions reactivate correctly once a pending extension is
  paid
- Member usage initialized at provisioning so the organization owner
  occupies the first seat immediately
- Organization layout checks pending invoices directly instead of
  duplicated billing-block logic; non-owners are redirected from
  owner-only Billing pages

## [0.1.0] - 2026-07-23

Initial release. See `README.md` for what Stratum is and `docs/` for
architecture, module, and API reference docs.

### Added

- Multi-tenant organizations: roles (owner/admin/member), member management,
  bulk CSV import, ownership transfer, email invitations, invite codes
- Organization settings, logo/file storage, IP allowlist
- Outbound webhooks: CRUD, per-delivery async retry via RabbitMQ (capped at
  50), delivery log, health tracking
- Audit log with CSV export, guarded against CSV formula injection
- Account profile, avatar, async GDPR data export/deletion, session
  revocation, TOTP MFA (fails closed if enrollment status can't be determined)
- Full subscription lifecycle with trial, proration, cancel/resume; plan and
  billing cycle required at organization creation
- Billing catalog: add-ons, coupons, multi-currency (USD/IDR) pricing,
  per-country tax
- Invoice PDF generation (EN/ID), Stripe (USD) and Xendit (IDR) payment links
- Usage metering and per-subject plan-based rate limiting
- Real-time (SSE) and email notifications, bilingual EN/ID, per-user
  preferences
- Reference data API: countries, currencies, plans/features/addons
- OpenAPI/Swagger docs for all API operations
- OpenTelemetry traces, metrics, and logs
- Health endpoints for liveness, readiness, and runtime stats
- SSRF-safe outbound HTTP client for webhook delivery; per-subject idempotency
  keys; CORS and webhook-secret verification fail closed outside `development`
- Web app: auth flow, onboarding wizard, org management, billing,
  notifications, account settings; EN/ID i18n; keyboard shortcuts
- Stratum Studio: standalone desktop admin console — project registry (OS
  keychain credential storage), dashboard, queue monitor, catalog CRUD,
  monitoring, support tools, watchlist, broadcast, organization ops, audit
  log, operator log

