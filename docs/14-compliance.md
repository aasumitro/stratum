# 14 · Compliance (SOC 2)

SOC 2 is a CPA firm's attestation that an organization's controls meet the AICPA Trust Services Criteria (TSC). Security (the Common Criteria, CC1–CC9) is always in scope; Availability (A1), Confidentiality (C1), Processing Integrity (PI1) and Privacy (P) are optional. A Type I report looks at one point in time, a Type II report at an observation window of usually 3–12 months.

**No codebase is SOC 2 compliant by itself.** Most criteria are organizational. This repository implements the technical controls an auditor tests, produces the evidence they sample, and documents who owns the rest. It targets Security, Availability and Confidentiality; Processing Integrity and Privacy are documented below but not extended.

## Shared responsibility

| The template provides | The adopting company owns |
|---|---|
| Authentication against Supabase JWTs, RBAC, row-level security, an organization IP allowlist | Written policies, risk assessment, HR controls, security training, management oversight |
| An append-only audit trail with a 365-day floor, and attribution of operator changes to a person | The auditor engagement and the observation window |
| Retention sweeps for personal data | Choosing retention periods that match its own legal obligations |
| Security metrics, and an alert catalog ([12-operations](12-operations.md)) | An OpenTelemetry backend that evaluates those alerts, and someone who answers them |
| Backup and restore tooling, and a restore check | Scheduling backups, off-host copies, and quarterly restore tests |
| CI with tests, dependency audits, vulnerability and secret scanning | GitHub settings: branch protection, required checks, required review, 2FA |
| Incident, access-review and secret-rotation runbooks | Running them, recording the results, and a `SECURITY.md` with a real reporting channel |
| — | Production infrastructure: cloud IAM, disk encryption, network, central log storage |
| — | Supabase project settings: password policy, leaked-password protection, session timeouts, MFA |
| — | Vendor management, including collecting each subprocessor's SOC report |

## Control matrix

Owner **T** means the template implements the mechanism; **A** means the adopter must. **T+A** means the template implements it and the adopter must configure or operate it.

| Criteria | Mechanism | Evidence source | Owner |
|---|---|---|---|
| CC1 Control environment | Policies, org structure, accountability | The adopter's own records | A |
| CC2 Communication | This document, [12-operations](12-operations.md), a `SECURITY.md` with a reporting channel | These files | T+A |
| CC3 Risk assessment | Risk register and its reviews | The adopter's own records | A |
| CC4 Monitoring of controls | CI on every pull request and `dev` push; the alert catalog | CI run history; alert history in the adopter's backend | T+A |
| CC5 Control activities | Controls below are defined in code and migrations, so they are reviewable | Git history, `db/migrations/` | T+A |
| CC6 Logical access | Supabase JWT validation, aal2 on high-impact routes, RBAC, `FORCE ROW LEVEL SECURITY`, per-plan rate limits, webhook signatures, HSTS, secrets from the environment only, one Postgres login per operator, Studio refusing unattributed changes | `middleware/auth.go`, `middleware/mfa_gate.go`, `db/migrations/000008`, `000010`, `000011`, the access-review query in [12-operations](12-operations.md) | T+A |
| CC7 System operations | `audit.events` append-only for runtime roles, and operator rows cannot be anonymized; failed provider webhooks recorded; dropped events counted; `govulncheck`, `golangci-lint` with `gosec`, `npm audit` and `gitleaks` in CI; incident runbook | `information_schema.role_table_grants`, `audit.events`, `stratum.*` metrics, CI runs | T+A |
| CC8 Change management | CI on pull requests to `main` and pushes to `dev`, pinned tool versions, versioned migrations, `CHANGELOG.md`, Dependabot | `.github/workflows/ci.yml`, `.github/dependabot.yml`, git history | T+A |
| CC9 Risk mitigation | Subprocessor list below; vendor reviews | This document; the adopter's vendor records | A |
| A1 Availability | Liveness and readiness probes, graceful shutdown, transactional outbox, per-module dead-letter queues, hourly reconciler, `make db-backup`, `make db-restore-verify` | `/health/ready`, `stratum.outbox.due_unpublished`, `stratum.dlq.messages`, recorded restore results | T+A |
| C1 Confidentiality | Webhook signing secrets encrypted with pgcrypto, Studio DSNs in the OS keychain, the retention sweeps below, audit field redaction | `platform/audit/redact.go`, `organization.webhook_endpoints`, the schedule below | T+A |
| PI1 Processing integrity | Money moves only on verified webhooks; idempotency table, row locks, transactional outbox, integration tests | `billing.webhook_events`, `billing.subscription_history`, CI runs | T |
| P Privacy | Async data export and account deletion; deletion anonymizes audit and billing records. Recording consent to terms is not built | `account.tasks`, `audit.events`, [03-modules](03-modules.md) | T+A |

The audit trail records every non-GET request, handler-flagged GETs, and provider webhook calls that fail. An unflagged GET writes no row, including one answered 401 or 403; `stratum.http.auth.failures` counts those instead.

**What append-only does not cover.** Runtime roles keep INSERT, so a stolen runtime credential can add false rows, including ones naming an operator, though it cannot change or remove real ones. A stolen `stratum_worker` credential can also anonymize a named customer's rows through `audit.anonymize_actor`, though not operator or `anonymous` rows. The schema owner can edit anything, and only the Postgres log shows it.

## Data inventory and retention

Every period below is the default and can be changed through its environment variable. Sweeps run hourly, drain everything past its cutoff, and log how many rows they removed, which is the evidence retention ran. A failed sweep retries on the next tick and increments `stratum.retention.sweep.failures`.

| Table | Personal data | Retention | Disposal |
|---|---|---|---|
| `account.users` | Email, name, avatar | Life of the account | Account deletion |
| `account.login_events` | IP address, user agent | `LOGIN_EVENT_RETENTION_DAYS`, 400 days | Worker sweep; also account deletion |
| `account.tasks.result` | A full personal-data export | `DATA_EXPORT_RETENTION_DAYS`, 7 days | Worker sweep clears the payload and keeps the task row |
| `audit.events` | Actor, IP address, user agent; before/after values in `metadata`. Operator rows' `metadata` leaves out invite codes, checkout URLs, organization owners and the person a coupon target or subscription is aimed at | `AUDIT_RETENTION_DAYS`, 400 days, minimum 365 | `audit.sweep_expired`, hourly from the API. Account deletion anonymizes the actor only; everything else stays until retention removes the row |
| `notification.messages` | Message content | `NOTIFICATION_RETENTION_DAYS`, 180 days | Worker sweep; also account deletion |
| `organization.invitations` | Email | Until expiry | Hourly expired-invitation cleanup; also organization purge |
| `organization.memberships` | The member's `auth_sub` | Life of the membership | Account deletion, member removal, organization purge |
| `organization.webhook_deliveries` | Customer response bodies | `WEBHOOK_DELIVERY_RETENTION_DAYS`, 30 days | Worker sweep; also organization purge |
| `organization.organizations` with `status = 'deleted'` | Organization name, owner `auth_sub` | `DELETED_ORGANIZATION_RETENTION_DAYS`, 30 days after deletion | Worker sweep, which cascades to memberships, invitations, webhook endpoints and deliveries |
| `billing.webhook_events` | None; replay markers | `WEBHOOK_EVENT_RETENTION_DAYS`, 90 days | Worker sweep |
| `messaging.outbox` | Event payloads | `OUTBOX_RETENTION_DAYS`, 30 days once published | Worker sweep |
| `billing.*` invoices, payments, subscriptions, history | Organization-level financial records | As long as the adopter's legal obligation requires | Never swept. Account deletion anonymizes history |

A purged organization's audit events stay until audit retention removes them, and its billing rows keep the organization id.

The plan catalog's `audit_retention_days` feature (30 days on Solo, 90 on Growth) is not enforced. Audit rows live for `AUDIT_RETENTION_DAYS` on every plan.

## Subprocessors

| Subprocessor | Purpose | Data it receives |
|---|---|---|
| Supabase | Authentication and object storage | Credentials, email, uploaded avatars and logos |
| Stripe | USD payments | Invoice amounts, payer details entered at checkout |
| Xendit | IDR payments | Invoice amounts, payer details entered at checkout |
| The adopter's SMTP provider | Transactional email | Recipient address, message content |
| The adopter's OpenTelemetry backend | Traces, metrics, logs | Telemetry; logs carry no secrets or credentials |
| The adopter's hosting provider | Compute and storage | Everything at rest |

MaxMind GeoLite2 is a local database file; no data leaves the host for a lookup.

## Production hardening checklist

**Environment and transport**
- [ ] `APP_ENV` is `staging` or `production`, so boot enforces every required secret.
- [ ] Postgres URLs use `sslmode=verify-full`, Redis `rediss://`, RabbitMQ `amqps://`. Boot logs a warning naming each variable that does not.
- [ ] The API is served only over TLS. It sends HSTS outside development, and that header is ignored over plain HTTP.

**Supabase**
- [ ] MFA enabled, minimum password length set, leaked-password protection on.
- [ ] Session time-box and inactivity timeout set.

**GitHub**
- [ ] Branch protection on `main` and `dev`: required checks, required review, no force-push.
- [ ] 2FA required for the organization.
- [ ] `SECURITY.md` added, with a reporting channel someone watches.

**Postgres**
- [ ] `ALTER ROLE <owner> SET log_statement = 'mod'`, so everything the owner role changes is logged. Owner sessions are not captured in `audit.events`.
- [ ] `log_connections = on`.
- [ ] One login role per operator, with an expiry, as in [12-operations](12-operations.md).
- [ ] The Postgres log is shipped off-host. An owner can edit audit rows; this log is what shows it happened.

**Backups**
- [ ] Backups are scheduled by the host or the provider, and copied off-host.
- [ ] A restore test runs every quarter and its result is recorded.
