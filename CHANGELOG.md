# Changelog

All notable changes to Stratum are documented here.
Format based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
versioning follows [SemVer](https://semver.org/).

## [Unreleased]

### Added

- `OUTBOX_RETENTION_DAYS` (optional, default `30`): hourly sweep deletes published
  `messaging.outbox` rows older than this many days; unpublished rows are never touched
- `WEBHOOK_SECRET_ENCRYPTION_KEY_PREVIOUS`/`WEBHOOK_SECRET_ENCRYPTION_KEY_VERSION`
  (optional): support a zero-downtime `WEBHOOK_SECRET_ENCRYPTION_KEY` rotation — see
  "Rotating the webhook encryption key" in `docs/12-operations.md`
- `AUTH_ACCESS_TOKEN_MAX_TTL` (optional, default `1h`): the maximum lifetime a
  Supabase-issued access token can have in this deployment; used to size the session
  revocation epoch's TTL

### Changed

- `POSTGRES_URL` is replaced by `POSTGRES_APP_URL`/`POSTGRES_WORKER_URL`/`POSTGRES_WEBHOOK_URL`
  for the app's own runtime connections; `WEBHOOK_SECRET_ENCRYPTION_KEY` and `STATS_TOKEN`
  are now required outside development. See "Upgrading an existing deployment" in
  `docs/12-operations.md` for the required order.

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

