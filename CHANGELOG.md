# Changelog

All notable changes to Stratum are documented here.
Format based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
versioning follows [SemVer](https://semver.org/).

## [Unreleased]

### Security

- Runtime roles can no longer write an audit row naming an operator. Only the database functions
  that record Studio's changes can (migration `000012`).
- Deleting an account now also clears the IP address and user agent on its audit rows.
- `audit.events` is append-only again. Runtime roles held UPDATE and DELETE on it through a later
  migration's blanket grant. Retention and GDPR anonymization now go through narrow database
  functions, and the 365-day retention floor is enforced in SQL.
- Every change made through Studio is attributed to a person. Each operator connects with their own
  Postgres login in the new `stratum_operator` group, and a trigger records the old and new row as
  `operator:<role>`, minus invite codes, checkout URLs, owner identifiers and any person a row is aimed
  at. Broadcasts and queue
  requeue/purge are recorded before they run, and do not run if the record fails. Operator rows cannot
  be anonymized. Studio refuses changes on any other connection and shows a persistent banner.
- Failed provider webhook calls now leave an audit row (no body captured).
- One request can no longer erase other requests' audit events. A batch is one statement, so a single
  value Postgres refuses, such as a non-UTF-8 `User-Agent` or a `\u0000` in a JSON body, lost the whole
  batch. The value is now cleaned or dropped, and only that row loses its metadata.
- HSTS is sent outside development, and boot warns about each datastore connection that would travel
  unencrypted.
- CI scans git history for secrets (`gitleaks`), audits npm dependencies, runs on `dev` pushes, and
  pins its Go tools. Dependabot opens weekly updates. `shadcn` moved to `devDependencies`, and
  `npm audit fix` cleared every high and critical advisory in both frontends.

### Added

- Studio lists payments to review: money a provider took that settled nothing (a payment on a void or
  failed invoice, or a second payment of one invoice). An operator refunds each at the provider and
  marks it refunded in Studio.
- Retention sweeps in the worker, each configurable: login events (400 days), GDPR export payloads
  (7), notifications (180), webhook deliveries (30), provider webhook markers (90), and a permanent
  purge of organizations soft-deleted more than 30 days ago.
- OpenTelemetry instruments: `stratum.audit.events.dropped`, `stratum.audit.write.failures`,
  `stratum.http.auth.failures`, `stratum.ratelimit.rejections`, `stratum.outbox.due_unpublished`,
  `stratum.outbox.exhausted`, `stratum.dlq.messages`, `stratum.retention.sweep.failures`. Audit events
  dropped on buffer overflow are now counted and logged.
- `make db-backup` (owner-only file permissions) and `make db-restore-verify`.
- `docs/14-compliance.md`: SOC 2 control matrix, data inventory and retention schedule,
  subprocessors, and a production hardening checklist. `docs/12-operations.md` gains incident
  response, operator access, secret rotation and an alert catalog.

### Changed

- **Breaking:** an expired checkout (Stripe `checkout.session.expired`, Xendit `EXPIRED`) is no longer a
  failed payment. Only its link closes: the invoice stays payable through a new checkout, and there is
  no failure notice, no dunning, no `past_due`, and no `billing.invoice.failed` customer webhook.
  Previously Stripe's expiry past-dued a subscription about two days before its period ended. A
  declined card or a failed bank debit still counts as a failure.
- Studio's Void gives back the coupon application the voided invoice's discount used, as the app's
  own void does.
- `npm test` in `ui/app` pins the locale and time zone.
- **Breaking:** `AUDIT_RETENTION_DAYS` defaults to 400 (was 30), and boot fails below 365. Raise it
  in any existing environment before upgrading.
- **Breaking:** migration `000011` needs the `stratum_operator` role to exist first
  (`deploy/postgres-init/01-app-role.sql`, or `docs/12-operations.md` for other environments).
- **Breaking:** Studio refuses changes unless its connection is an operator login. Switch each
  project's DSN to one (locally, `stratum_operator_dev` from `deploy/postgres-init/03-operator-role.sql`).
- **Breaking:** while an organization's first invoice is unpaid, the API refuses writes with
  `403 ACTIVATION_UNPAID`, for every member and every client. This was enforced only by the web app. Reads,
  Billing, deleting the organization and leaving it stay open.
- **Breaking:** `PATCH /billing/plan` refuses a lower plan with `422 PLAN_CHANGE_IS_DOWNGRADE`, on every
  status. `POST /billing/downgrade` serves all of them.
- Cancelling is scheduled on every status, trials included: access continues until the end of the
  paid period (or the trial) and the cancel can be undone until then. A pending renewal invoice is
  voided when the cancel is requested; Undo issues it again.
- The `billing.subscription.cancelled` event — and the outbound customer webhook of the same name —
  now fires when a cancellation takes effect at the end of the period, at the same moment the
  organization is suspended, instead of when it is requested.
- Resume (from `cancelled` or `expired`) always issues an invoice first and reactivates only once it
  is paid; a repeat request reuses the pending invoice. Resuming inside a trial no longer returns the
  subscription to `trialing`.
- While a cancellation is scheduled, a downgrade, an add-on decrease or removal, and activate-now
  are refused with `422 CANCELLATION_SCHEDULED`.
- A plan change with the first (activation) invoice still unpaid reissues the activation at the new
  price and never lengthens the period; paying it grants one full cycle from the period's start.
  Pending extension and add-on-increase invoices are voided rather than turned into a renewal.
- A pending renewal invoice no longer blocks the organization's navigation in the web app — only an
  unpaid activation invoice does.

### Fixed

- A payment that settled nothing (on a void or failed invoice, or a second payment of one invoice) was
  only logged. It is now recorded as a payment, and the owner's payment list shows it.
- Member usage read an old count after an upgrade moved `period_end` earlier, so limit checks for
  invites, adds and joins used the count from before the upgrade.
- Previewing a downgrade and downgrading held a database connection while overage resolution waited
  for a second one from the same pool. Under load, enough of them at once stalled every request.
- The outbox relay's integration tests published the dev database's due rows to a fake publisher, so
  they never reached the broker.
- An owner could keep a paid plan without paying by repeating a plan change while the activation
  invoice was unpaid, which restarted the period each time.
- An owner could dodge a billing suspension by suspending the organization just before the period
  ended, then lifting it. A billing suspension now replaces the owner's own.
- An owner could grow a paid period without limit by switching cycle on the same plan and then moving
  between plans, because proration read the old price at whichever rate the free switch had just set.
  A plan change can now only keep or shorten the period.
- A provider reporting a checkout we had closed ourselves counted as a failed payment: the subscription
  went `past_due` and the owner got dunning notices after any plan change, cancel or repeat checkout,
  and even after paying.
- A payment landing just after a plan change on an expired subscription reactivated it on the new plan
  and cycle, so a month's price could buy a year on a dearer plan. It is now logged and left for manual
  reconciliation, like any other payment nothing applies automatically.
- Paying the activation invoice in the last three days of the period left no renewal invoice, so the
  subscription expired at the end of a period the owner had paid for.
- An add-on increase while the activation invoice was unpaid rode a cycle it was not priced for, and a
  plan change reissuing that invoice then charged it twice. It is now refused with
  `422 PERIOD_INVOICE_PENDING`.
- Adding to an expired or cancelled subscription while its Resume invoice was pending applied at no
  charge, and paying that invoice then brought the add-on back free for a whole cycle. It is now refused
  with `422 PERIOD_INVOICE_PENDING`.
- An add-on raised while a renewal was pending rode that cheaper renewal for a whole cycle, every cycle.
  Paying the increase now reissues the renewal at the new quantity. A trial's add-on changes do the same
  for the invoice converting the trial.
- An invoice priced from a state that has since changed stayed payable. A paid renewal or extension now
  voids the other one and any pending add-on increase, and a paid add-on increase drops a pending
  extension.
- A period under half a month, which an upgrade can leave, rounded to zero months and made an add-on
  increase free.
- `POST /organizations` accepted a plan with no price, which meant a free period of it with no invoice,
  on the unlimited `custom` plan. `PATCH /billing/plan` accepted it too, and a lower plan, which skipped
  resolving the members over its limit.
- A pending add-on increase or extension stopped the renewal invoice, so the subscription could expire
  with nothing to pay.
- A card declined on a checkout already replaced marked the open one failed and moved the subscription to
  `past_due`. The checkout now carries its own identity in the PaymentIntent's metadata.
- A coupon covering a whole charge failed activate-now, Resume and a 2nd+ organization's creation with a
  500, let a paid-up subscription lapse at renewal, and voided an unpaid activation with no replacement.
  The charge now has the effect a payment would, with no invoice. A plan with no price and no coupon answers
  422 on activate-now, Resume and Extend instead of 500.
- A voided or failed invoice kept the coupon application it used, so what replaced it lost a once-only
  discount.
- A coupon absorbing an add-on increase was not used by it, so a once-only coupon paid for one increase after
  another and was still there for the renewal. A fixed-amount coupon no longer applies to an increase at all,
  since one split into many small invoices would be absorbed seat by seat.
- A once-only coupon could be redeemed again on the same subscription after it was used up.
- Activate-now in a trial's last three days left the conversion invoice open: paying both bought two cycles,
  and either one left open dunned or blocked an organization that had paid. It now replaces that invoice.
- A scheduled removal of an add-on left its pending increase payable, and paying it after the renewal removed
  the row lost the money. A decrease or removal now voids that add-on's pending increase.
- Requests side by side issued duplicate invoices: up to one per delivery of a duplicate auto-invoice message,
  and one per click on Extend or activate-now. Invoices are now decided and issued under the subscription's row
  lock, which also stops a price missing an add-on change in flight, and a plan change overwriting the period a
  payment in flight just bought.
- A late payment reactivating a subscription left a Resume invoice open, and that checkout expiring moved
  the fresh period to `past_due`. A late payment for an invoice issued before an add-on grew is now left for
  manual reconciliation, like one issued before a plan change.
- A redelivered `organization.created` issued the first invoice again if the first one had been paid.
- `POST /billing/downgrade` answered 500 for an unknown plan.
- A request body holding a number too large for a float skipped audit redaction and was stored as
  sent. It is now stored as empty metadata.
- Studio's Extend Trial always failed with an encoding error.
- Studio's queue requeue lost a message that had no properties: it failed decoding the response after
  the message had already left the queue.
- Editing a project's DSN in Studio kept the old connection until the app restarted.
- The audit retention sweep ran on the goroutine that drains the event buffer, so a slow sweep could
  drop events under load.
- A plan change that moved `period_end` never rescheduled renewal, so no renewal invoice was
  created and the subscription expired at the new end.
- Paying an activation invoice after a failed or expired checkout left the subscription `past_due`.
- A scheduled cancellation suspended the organization three days before the paid period ended.
- A cancelled trial kept access forever, and resuming a cancelled subscription granted a new period
  without payment while leaving the organization suspended.
- A plan change reactivated an `expired` subscription (and cleared `past_due`) without payment, and
  turned an unpaid activation invoice into a renewal that added a free cycle once paid.
- An expiry check racing a committed renewal could overwrite it with `expired`.
- A resumed subscription kept its old cancellation schedule and was cancelled again at its next
  renewal.
- Renewal reminders and auto-invoices built from a nanosecond clock were discarded as stale on Linux.
- Stripe `checkout.session.async_payment_succeeded` / `async_payment_failed` are now applied instead
  of dropped.
- The hourly overdue-subscription reconciler now also ends `past_due` subscriptions.
- Audit: request bodies over 4 KB reach the handler whole, and one malformed body no longer drops the
  whole batch of audit events.
- Invitations are stored with a lowercase email, so a mixed-case invite is visible to the invitee and
  a second casing cannot create a duplicate.
- The notification stream sends its headers at connect, so the browser sees it open immediately, and
  a successful reconnect resets the client's failure count.
- Payment-provider calls (resume, extend, activate, add-on attach, pay) and the members import no
  longer time out client-side after 5 seconds.
- A cancelled organization's owner sees the billing banner with a link to Billing instead of an
  Unsuspend button that always failed.
- Studio's "Cancellations taking effect" watchlist lists scheduled cancellations again, and
  "Renewals due" no longer includes subscriptions that will not renew.
- An extension paid after a failed renewal had made the subscription `past_due` was dropped; it now
  adds its months and returns the subscription to `active`.
- The expiry notification and email told the owner to pay an invoice that expiry had already
  closed; they now point to Resume.
- Notifications whose copy has no variables (subscription cancelled/expired, organization
  reactivated) were always shown in English; they now follow the viewer's language.
- The suspend/unsuspend toasts showed raw translation keys.
- Unsuspend (Settings danger zone and the owner's suspension banner) is offered only for the owner's
  own pause: a billing suspension links to Billing and a support suspension says to contact support,
  instead of a button the API always refused.
- Studio no longer offers Activate on a cancelled subscription: that state now means the period has
  ended and the organization is suspended, so it returns through Resume and payment.
- A payment that arrives for a closed invoice of an ended subscription — an activation failed at
  expiry or when a cancellation took effect, or a renewal voided by a cancel request or a plan
  change — now reactivates it like a Resume payment instead of only being logged. A deleted
  organization's subscription stays ended.
- Checkouts left open at Stripe/Xendit when an invoice was voided, failed or superseded by a new
  link could still take money; every local link expiry now closes the checkout at the provider
  too, through the outbox and the billing worker. Once an invoice is paid — by webhook or by a
  Studio mark-paid — its other open checkouts are closed the same way, so it can't be paid twice.
- A paid reactivation lifted any suspension, including one set by a Studio operator; it now lifts
  only billing's own, and "organization reactivated" is only announced when it actually was.
- Studio "Mark paid" only flipped the invoice; it is now applied by the billing worker like a real
  payment (period, reactivation, billing suspension, next renewal) and shows as "Support" in the
  subscription history. Studio "Void" also closes the invoice's open checkouts.
- `POST /organizations/:id/suspend` accepted a custom reason the owner could then never lift; it
  now ignores any body and always records the owner's pause.
- An open notification stream held API shutdown for the full 15 s graceful timeout, which then
  logged "startup failed"; streams now end as shutdown starts, and the exit log says the process
  stopped with an error.

### Removed

- The unused `BillingGate` middleware and `OrganizationReader.IsMember`.

## [0.5.0] - 2026-10-23

Row-level security extended from `billing` to the `organization` and `notification` schemas, the
scheduled billing lifecycle (expiry check, renewal reminder, dunning notices) delivering again,
per-member suspend, and the verified hardening follow-ups of an external backend audit.

### Added

- Per-member suspend / reinstate: `POST /organizations/:organizationID/members/:authSub/suspend`
  and `.../reinstate` (admin or owner). A suspended member keeps their role and membership row
  but loses access to the organization and stops occupying a seat; reinstating is subject to the
  seat limit at that moment. `GET .../members` returns each member's `status`, the web app's
  Suspend button is live, and the member gets an in-app notice on both transitions
- `POSTGRES_UNSCOPED_URL` (required, both binaries): a fourth runtime role,
  `stratum_app_unscoped` (`BYPASSRLS`, used by `cmd/api` only), for the organization paths that
  have no tenant to scope a transaction to — the organization middleware's authorization lookups
  and the cross-org invite/join flows. Sized by `POSTGRES_UNSCOPED_MAX_OPEN_CONNS` (optional,
  default `5`)
- `POSTGRES_STATEMENT_TIMEOUT` (optional, default `30s`, `0` disables): a per-statement timeout
  on every Postgres pool, so a runaway query can't hold a connection indefinitely
- Semantic audit actions for three privileged mutations, previously logged only as their HTTP
  method: `webhook.secret_rotated`, `invite_code.regenerated`, `member.removed`
- Staging stack: a `Dockerfile` for the api + worker image and `deploy/compose.staging.yml` with
  `deploy/.env.staging.example` for `podman compose`. The GeoLite2 database is bind-mounted, never
  baked into the image
- Public event keys for the checked billing notices notification sends:
  `billing.subscription.expiring-soon`, `billing.invoice.payment-reminder`,
  `billing.invoice.payment-final-notice`

### Changed

- **Upgrade order for an existing database** (both migrations ship in this release): create
  `stratum_app_unscoped` and set `POSTGRES_UNSCOPED_URL` on both binaries, apply only `000009`
  (`migrate ... up 1`), deploy the new build, then apply `000010`. Applying `000010` before the new
  build runs hides every `organization`/`notification` row from the old one — see "Provisioning
  database roles" in `docs/12-operations.md`
- **A database migrated by 0.4.0 needs a rebuild**: `000002_organization` was edited in place to
  add `organization.memberships.status`, so `make migrate-up` alone never adds it. Rebuild with
  `make migrate-down-all && make migrate-up`, or add the column by hand:
  `ALTER TABLE organization.memberships ADD COLUMN status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended'));`
- The 8 `organization` and `notification` tables now run under `FORCE ROW LEVEL SECURITY`.
  The API scopes each operation to one transaction that sets the organization
  (`app.organization_id`) or the caller (`app.auth_sub`) instead of holding one transaction for
  the whole request, and an operation can't scope itself to an organization other than the one
  the organization middleware authorized. `notification.processed_events` is closed to the API
  role entirely. Studio's DSN must be the schema owner or a `BYPASSRLS` role, or it sees no rows
  in these schemas
- Delayed billing events are future-dated outbox rows (`not_before`) delivered by the relay like
  any other event; the `billing.delay` exchange and its five `billing.*-delay` parking queues are
  retired. Rewrite unpublished rows still addressed to them before upgrading — see "Delayed
  billing events left on the retired delay topology" in `docs/12-operations.md`
- The renewal reminder and the day-3/day-7 dunning notices are each split into a scheduled key
  that only billing consumes and a checked key that notification sends. Billing drops a reminder
  whose subscription is no longer active/trialing or whose end date moved, and a dunning notice
  whose invoice is no longer pending, isn't period-backing, or whose subscription has ended.
  Delete the retired `notification.subscription-remind`, `notification.subscription-payment-remind`
  and `notification.subscription-payment-final` queues on an existing broker (still bound to the
  scheduled keys) — see `docs/12-operations.md`
- A `past_due` subscription now expires at the end of its period like an active one: its pending
  invoices become `failed` and their payment links `expired`, and the organization is suspended.
  A failed payment moves a subscription to `past_due` only for a `subscription`/`activation`
  invoice; a failed add-on-increase or extension payment leaves the subscription's status alone
- A `failed` invoice is terminal in the web app: dimmed like a `void` one, with no pay or
  regenerate action
- The billing page shows one banner per billing state instead of up to five stacked ones saying
  the same thing; banners no longer link to the page they're on, and a lapsed period is no longer
  shown as the "next renewal"
- The Members and Billing notification-preference toggles now also cover invitations and
  subscription-expired notices. A registered invitee's invite email and in-app notice honour that
  preference; an unregistered invitee always gets the email
- Organization settings reject a timezone the IANA database can't resolve (`INVALID_TIMEZONE`)
  and a locale outside the BCP-47 `language(-REGION)` shape (`INVALID_LOCALE`). The API and worker
  binaries embed the time zone database, so this doesn't depend on the base image
- Input bounds: add-on quantity is capped at `10000`; a cached value over 1 MiB is rejected
  before it reaches Redis
- `POSTGRES_BACKGROUND_MAX_OPEN_CONNS` and `POSTGRES_UNSCOPED_MAX_OPEN_CONNS` must each be above
  `0` and below `POSTGRES_MAX_OPEN_CONNS`, or the API refuses to boot
- GDPR account deletion: a failed critical cleanup step (notifications, memberships, audit log,
  billing history) now leaves the task in progress for a retry instead of reporting the account
  erased while that personal data still exists; avatar and login-event cleanup stay best-effort
- A rate-limiter backend error is now logged (the request is still let through)
- Go 1.27 toolchain; the standard library `uuid` package replaces `github.com/google/uuid`.
  Dependencies updated to fix a reported Go vulnerability (pgx, amqp091-go, go-redis,
  OpenTelemetry, among others)

### Fixed

- Scheduled billing events — the end-of-period expiry check, renewal reminder, auto-invoice, and
  day-3/day-7 dunning notices — were never delivered since the outbox landed in 0.4.0: the relay
  parked them in `billing.delay` queues that had no TTL and no consumer
- Once the renewal reminder did deliver, an owner got one "expiring soon" email + in-app notice
  per relay tick until the subscription changed, plus an unchecked duplicate that used the paid
  wording even for a trial
- A `past_due` subscription never expired, so its organization was never suspended and the
  day-7 notice's warning was never carried out
- A renewal payment that completed at the gateway while the period-end expiry was failing its
  invoice now restores the subscription instead of being stranded on a `failed` invoice
- Dunning notices could go out for an invoice that was already paid, voided, or failed, or that
  doesn't back the subscription's period
- The AMQP publisher's reconnect goroutines now stop on shutdown instead of retrying a dead broker
  through it
- A panic in Studio's monitor poller no longer crashes the whole app

### Removed

- `POST /organizations/:organizationID/billing/invoices/:invoiceID/pay/regenerate` and the web
  app's Regenerate button

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

