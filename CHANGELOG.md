# Changelog

All notable changes to Stratum are documented here.
Format based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
versioning follows [SemVer](https://semver.org/).

## [Unreleased]

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

