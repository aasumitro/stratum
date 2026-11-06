# 12 · Operations

How Stratum is deployed, kept healthy, and recovered when something goes wrong.

## Topology
Two Go binaries (`api` and `worker`) run alongside three infrastructure services — PostgreSQL 18, RabbitMQ 4, and Redis 8 (containers; `deploy/docker-compose.local.yml`). An OpenTelemetry collector is optional (`deploy/otel/`). External dependencies are Supabase (auth + storage), Stripe/Xendit (payments), and an SMTP server (email).

```
Web ──HTTPS──► api ──► Postgres / Redis
                 └─publish─► RabbitMQ ──► worker ──► Postgres / SMTP / Stripe·Xendit
Studio ──direct DSN──► Postgres / RabbitMQ / Redis   (bypasses the API)
```

## Configuration
All configuration is read from the environment once at startup and passed explicitly through the app — never read deep inside a module. The required values are the three infra URLs plus the JWKS URL and issuer; payments, SMTP, storage, and telemetry are optional and degrade gracefully when unset. Secrets live in `.env.local` and are never committed or logged. See `02-getting-started.md` for the full variable list.

## Deploy sequence
```bash
cp .env.example .env      # fill required values
make infra-up
# provision database roles (including the stratum_operator group) here — staging/production only, see
# "Provisioning database roles" below (local dev already did this
# automatically when the Postgres container first booted)
make migrate-up
make build                # bin/api + bin/worker
make run-api
make run-worker
```

**Provisioning database roles**: role creation is deliberately not part of the migration
pipeline (`ADR-0026`) — `db/migrations/000008` only grants privileges to `stratum_app`,
`stratum_worker`, and `stratum_webhook`, `000009` to `stratum_app_unscoped`, and `000011` to
`stratum_operator`; they never create them. This is what lets
`make migrate-up` run without the migrator role itself needing `BYPASSRLS` or `CREATEROLE` —
PostgreSQL only permits a role that already holds `BYPASSRLS` to grant `BYPASSRLS` to a role
it creates, so embedding `CREATE ROLE ... BYPASSRLS` inside a migration would force every
migrator, including a deliberately unprivileged production one, to hold it too.

Local dev and CI get this for free: `deploy/postgres-init/01-app-role.sql` creates all five
roles, and either runs automatically (local dev, mounted into the Postgres container's
`docker-entrypoint-initdb.d`) or as an explicit CI step before migrations. Every other
environment needs a human to run the equivalent once, with a superuser-equivalent connection,
**before the first `make migrate-up`** — copy `deploy/postgres-init/01-app-role.sql`'s
`CREATE ROLE` statements, substituting real generated passwords for its hardcoded local-dev
ones:
```sql
CREATE ROLE stratum_app LOGIN PASSWORD '...';
CREATE ROLE stratum_worker LOGIN PASSWORD '...' BYPASSRLS;
CREATE ROLE stratum_webhook LOGIN PASSWORD '...' BYPASSRLS;
CREATE ROLE stratum_app_unscoped LOGIN PASSWORD '...' BYPASSRLS;
CREATE ROLE stratum_operator NOLOGIN;
```
**An environment that already ran an older build** needs `CREATE ROLE stratum_operator NOLOGIN`
before the migrate step that applies `000011`, whose first `GRANT … TO stratum_operator` fails
otherwise. A local database runs both scripts by hand, for example
`podman exec -i stratum-postgres psql -U stratum -d stratum < deploy/postgres-init/01-app-role.sql`,
then the same with `03-operator-role.sql`. Then deploy the new API and worker, and switch Studio to per-operator logins. Any
`AUDIT_RETENTION_DAYS` below 365 now fails boot, so raise it first. To roll back, redeploy the
previous binaries, then `make migrate-down`: older binaries delete audit rows directly, which needs
the privileges the down migration restores.

`stratum_app_unscoped` is used by `cmd/api` only (`POSTGRES_UNSCOPED_URL`, sized by
`POSTGRES_UNSCOPED_MAX_OPEN_CONNS`) for the organization paths that have no tenant to scope a
transaction to: the organization middleware's authorization lookups and the cross-org
invite/join flows. Both binaries require `POSTGRES_UNSCOPED_URL` set. **An environment that
already ran an older build needs only that one `CREATE ROLE` and the env var, before deploying
the build that ships migration `000009`** — `000009`'s `GRANT … TO stratum_app_unscoped` fails
with `role "stratum_app_unscoped" does not exist` otherwise.

That build ships `000009` and `000010` together, and they must not land at the same moment as the
code switch. Upgrade an existing environment in this order:
1. Create `stratum_app_unscoped` (above) and set `POSTGRES_UNSCOPED_URL` on both binaries.
2. Apply only `000009` (grants — harmless to the running build):
   `migrate -path db/migrations -database "$POSTGRES_URL" up 1`.
3. Deploy the new API + worker build. It works with or without `000010`'s policies.
4. Apply `000010` (`make migrate-up`). Never before step 3: under its policies, an older build
   sets no scope and sees no `organization`/`notification` rows.

The `organization` and `notification` tables run under `FORCE ROW LEVEL SECURITY` (`000010`),
like `billing`'s. Two consequences for operators:
- **Studio's DSN must be a `BYPASSRLS` role, and only an operator login can change anything.**
  Studio reads and writes these tables with no tenant scope (support tools, broadcasts); under
  `stratum_app` it would see nothing. See "Operator access" below.
- **A data migration on these tables must scope itself.** `FORCE` also binds a non-superuser
  table owner: set `app.organization_id` (`SELECT set_config('app.organization_id', '<org id>',
  true)`) per organization inside the migration's transaction, or wrap the change in `ALTER
  TABLE … NO FORCE ROW LEVEL SECURITY` / `… FORCE ROW LEVEL SECURITY`.
If this step is skipped, `make migrate-up` fails immediately and clearly on `000008`'s first
`GRANT ... TO stratum_app` with `role "stratum_app" does not exist` — run the step above, then
retry.

**Upgrading an existing deployment**: `POSTGRES_URL` (the app's old single runtime DSN) is
replaced by three role-scoped DSNs — `POSTGRES_APP_URL`, `POSTGRES_WORKER_URL`,
`POSTGRES_WEBHOOK_URL` — and `WEBHOOK_SECRET_ENCRYPTION_KEY` (required) plus `STATS_TOKEN`
(recommended) are now required/expected outside development; `config.Load()` fails startup
if `WEBHOOK_SECRET_ENCRYPTION_KEY` is unset or empty. `POSTGRES_URL` itself is unchanged as
the admin DSN `make migrate-up` runs against (see `DATABASE_URL ?= $(POSTGRES_URL)` in the
Makefile) — only the app's own runtime connections moved. Apply in this order:
1. Provisioning database roles (above), if not already done for this environment — migrations
   no longer create the `stratum_app`/`stratum_worker`/`stratum_webhook` roles as part of their
   DDL, so this step must complete before the next one, not as a side effect of it.
2. Run `make migrate-up` against `POSTGRES_URL`, using a role that owns the schemas (no longer
   needs `BYPASSRLS` or `CREATEROLE`).
3. Set `POSTGRES_APP_URL`, `POSTGRES_WORKER_URL`, `POSTGRES_WEBHOOK_URL`,
   `WEBHOOK_SECRET_ENCRYPTION_KEY`, and `STATS_TOKEN` in the environment for `api` and
   `worker`.
4. Start `api` and `worker` only after steps 1-3 complete.

Storage buckets auto-create on startup. Verify with `GET /health/ready` (expect 200) and confirm the worker registered its consumers.

## Health and observability
Three endpoints: `/health` (liveness), `/health/ready` (readiness — returns 503 when a dependency is degraded), and `/health/stats` (goroutines, memory, GC, connection-pool stats). `/health` and `/health/ready` are always safe to expose publicly; `/health/stats` leaks process internals and is gated by the `STATS_TOKEN` env var — set it (any non-empty value) and enter the same value in Studio's project form so its monitoring page can keep polling it. An empty `STATS_TOKEN` leaves the endpoint open (fine for local dev, logged as a startup warning outside development). Telemetry (traces, metrics, logs) is exported via OpenTelemetry when a collector URL is configured, and is a no-op otherwise. Stratum Studio's monitoring page polls the health endpoints and keeps history.

### Alert catalog
Stratum emits these signals but evaluates none of them. Configure these conditions in whichever backend receives its metrics.

| Condition | Severity | What it means |
|---|---|---|
| `stratum.audit.events.dropped` > 0 in 5 min | high | The audit buffer overflowed, so the audit trail has a gap |
| `stratum.audit.write.failures` > 0 in 5 min | high | The database refused a batch of audit events |
| `stratum.http.auth.failures{status=401}` above 5× its 7-day hourly baseline | medium | Possible credential stuffing or token probing |
| any `stratum.http.auth.failures` on a `/webhooks/*` route in 15 min | medium | A forged or misconfigured provider callback |
| `stratum.outbox.due_unpublished` > 100 for 10 min | high | Event delivery has stalled: rows are due or failed and waiting to retry |
| `stratum.outbox.exhausted` > 0 | high | Events were abandoned after every retry, and only an operator can recover them; see "Stuck outbox backlog" |
| `stratum.retention.sweep.failures` > 0 in 2 h | medium | A retention sweep failed, so data may be outliving its documented retention; the `module` attribute and the worker or API log say which |
| `stratum.dlq.messages` > 0 for 15 min | medium | Messages are dead-lettered; see "Dead-letter backlog" |
| `/health/ready` not 200 for 2 min | high | A dependency is degraded |

`stratum.ratelimit.rejections{limiter}` counts 429s per limiter, for dashboards rather than paging.

## Backups and data
PostgreSQL is the system of record. Redis is disposable cache (roles, rate-limit counters, the token blocklist) — losing it degrades, it doesn't corrupt. RabbitMQ holds in-flight events; a dead-letter backlog is recoverable. Supabase Storage holds user files and should be backed up separately if required.

The documented targets are **RPO 24 hours and RTO 4 hours**. Change them here if your SLA differs.

On a managed Postgres, use the provider's point-in-time recovery and take its backups as the source for the restore test. For a self-managed Postgres:

```bash
make db-backup                                # writes backups/stratum-<UTC timestamp>.dump, readable only by you
make db-restore-verify FILE=backups/<file>    # restores into a scratch database and checks it
```

`db-restore-verify` restores into a scratch database (`stratum_restore_verify`, dropped afterwards) and prints one line per check: `pg_restore` exits 0, the migration version matches the live database, and `ref.countries`, `ref.currencies` and `billing.plans` have rows. It ends with `RESULT: PASS`, or `RESULT: FAIL` and exit code 1. It proves integrity, not row-for-row equality. The app has no scheduler, so a systemd timer or launchd job on the host runs `db-backup` at least daily and copies each dump off the host. Run a restore test **every quarter and record the result**: an auditor asks for that record.

## Runbooks

**Dead-letter backlog** — In Studio's queue monitor, inspect the messages, fix the root cause, then requeue (FIFO) or purge poison messages. Consumers are idempotent, so requeue is safe for a *transient* failure. One `billing.events.dlq` cause is not transient: an `organization.created` event whose cart-selected coupon was exhausted by someone else before provisioning ran — the organization exists (its create request already succeeded) but has no subscription. Requeuing retries the same now-exhausted coupon and fails identically; the fix is to provision the subscription manually via Studio (without the coupon, or with a different one) and purge the dead-lettered event, not requeue it. `stratum.dlq.messages` reports each dead-letter queue's depth (see the alert catalog).

**Failing customer webhook** — Endpoints auto-disable after a three-day, 100%-failure window and warn below 70% success over 24 hours. Re-enable is only possible via a passing test event, not a direct toggle. Use the deliveries panel to diagnose, then bulk-retry failed deliveries once the receiver is fixed.

**Payment reconciliation** — Billing state only advances on a verified gateway webhook. If a customer paid but nothing changed, check that the webhook was received (and its signing secret), then use Studio's support tools to mark the invoice paid as a manual override (which is logged).

**Degraded readiness** — A 503 from `/health/ready` means a dependency check failed; `/health/stats` shows pool and GC pressure. Check Postgres, Redis, and RabbitMQ reachability. Don't restart infrastructure from inside an automated session — surface it to a human.

**Stuck outbox backlog** — Every domain event is written to `messaging.outbox` in the same transaction as the state change it describes, then delivered to RabbitMQ by a `cmd/worker` relay polling every 2 seconds. This alert and the next one are two distinct conditions — check `attempts` before concluding which one you're looking at; a row that has exhausted its retries (`attempts >= 20`) is not "delayed", it is abandoned and will never deliver.

*Delivery delayed, nothing lost* — rows still retrying:
```sql
SELECT count(*) FROM messaging.outbox WHERE published_at IS NULL AND attempts < 20 AND (not_before <= now() OR attempts > 0) AND created_at < now() - interval '5 minutes';
```
A nonzero result means the relay is stuck (worker process down or wedged) or the broker is unreachable — these events are durably queued and will deliver once the relay recovers. Check the worker process is running and can reach RabbitMQ; `attempts`/`last_error` on the oldest unpublished rows show the last failure the relay recorded for them.

*Events abandoned, manual replay required* — rows that exhausted every retry:
```sql
SELECT count(*) FROM messaging.outbox WHERE published_at IS NULL AND attempts >= 20;
```
A nonzero result here means these events were **not** delivered and never will be without operator action — the relay gave up on them (a malformed payload, a permanently-unroutable key), logged each one at `ERROR` (`"outbox: row exhausted, ABANDONED — will never be delivered"`), and moved on. Nothing is queued for later delivery. Inspect `last_error` on the affected rows, fix the underlying cause, then replay:
```sql
UPDATE messaging.outbox SET attempts = 0, not_before = now() WHERE id = ANY(:ids);
```

*Delayed billing events left on the retired delay topology* — run once per environment that ran a build from before delayed events were consolidated onto the outbox. Those builds enqueued the expiry check, renewal reminder, auto-invoice, and dunning notices onto the `billing.delay` direct exchange, whose parking queues had no TTL and no consumer, so every such row parks undelivered under a short legacy routing key. Rewrite the unpublished ones onto the real exchange and routing keys:
```sql
UPDATE messaging.outbox
   SET exchange = 'billing.events',
       routing_key = CASE routing_key
         WHEN 'subscription-check'          THEN 'billing.subscription.check'
         WHEN 'subscription-remind'         THEN 'billing.subscription.remind'
         WHEN 'subscription-auto-invoice'   THEN 'billing.subscription.auto-invoice'
         WHEN 'subscription-payment-remind' THEN 'billing.subscription.payment-remind'
         WHEN 'subscription-payment-final'  THEN 'billing.subscription.payment-final'
       END
 WHERE published_at IS NULL
   AND routing_key IN ('subscription-check', 'subscription-remind', 'subscription-auto-invoice',
                       'subscription-payment-remind', 'subscription-payment-final');
```
Rows already published into the legacy parking queues are not recoverable this way — the hourly `ReconcileOverdueSubscriptions` sweep covers the subscription-expiry consequence; a missed one-time reminder or dunning notice is accepted loss. Afterward the five legacy `*-delay` parking queues (`billing.subscription-check-delay`, `-remind-delay`, `-auto-invoice-delay`, `-payment-remind-delay`, `-payment-final-delay`) and the `billing.delay` exchange are unused and can be deleted (`rabbitmqctl delete_queue billing.subscription-check-delay` … for the queues; `rabbitmqctl` has no exchange-delete command, so remove the exchange with `rabbitmqadmin --username … --password … delete exchange --name billing.delay` (management plugin) or the management UI); leaving them is harmless, nothing publishes to them. Check first that `SELECT count(*) FROM messaging.outbox WHERE exchange = 'billing.delay' AND published_at IS NULL` is 0 — run the rewrite above otherwise, or those rows will fail to publish once the exchange is gone.

*Retired `notification.subscription-remind` queue* — run once per environment that ran a build from before the renewal reminder was split into a scheduled key and a checked key. Notification now consumes the checked reminder (`billing.subscription.expiring-soon`) from `notification.subscription-expiring-soon`. The old queue has no consumer but is still bound to the scheduled `billing.subscription.remind`, so it keeps collecting an unchecked copy of every scheduled reminder. Delete it — the messages in it are exactly those unchecked copies, so discarding them is correct:
```sh
rabbitmqctl delete_queue notification.subscription-remind
```
Builds in that window also re-delivered each checked reminder back to billing's own queue, so an owner could receive one "expiring soon" reminder per relay tick until the subscription's status or end date changed. To see whether an environment was affected, count the checked re-publishes, which are immediate rows under the scheduled key:
```sql
SELECT payload->>'org_id' AS org_id, count(*) AS republishes, min(created_at), max(created_at)
  FROM messaging.outbox
 WHERE routing_key = 'billing.subscription.remind'
   AND not_before <= created_at + interval '1 second'
 GROUP BY 1 ORDER BY 2 DESC;
```
The retention sweep deletes published rows after `OUTBOX_RETENTION_DAYS`, so run it before they age out.

*Retired `notification.subscription-payment-remind` / `notification.subscription-payment-final` queues* — run once per environment that ran a build from before the day-3/day-7 dunning notices were split into a scheduled key and a checked key. Billing now consumes the scheduled rows (`billing.subscription.payment-remind` / `-final`) and sends a notice on only while its invoice is still pending, backs the subscription's period, and the subscription has not ended. Notification consumes the checked notices (`billing.invoice.payment-reminder` / `billing.invoice.payment-final-notice`) from `notification.invoice-payment-reminder` / `notification.invoice-payment-final-notice`. The two old queues have no consumer but are still bound to the scheduled keys, so they keep collecting an unchecked copy of every scheduled notice. Delete them — the messages in them are exactly those unchecked copies, so discarding them is correct:
```sh
rabbitmqctl delete_queue notification.subscription-payment-remind
rabbitmqctl delete_queue notification.subscription-payment-final
```

**Rotating the webhook encryption key** — `WEBHOOK_SECRET_ENCRYPTION_KEY` rotation is a
4-step, operator-driven procedure; do not skip the verification step. Do not roll back the
application between steps 1 and 3 once the backfill has started — a rollback to the pre-rotation
release makes every already-rotated row fail to decrypt (`_PREVIOUS` is unset on that release, so
the `ELSE` branch of the key-selection `CASE` gets an empty passphrase and `pgcrypto` hard-errors).
Roll forward instead.

1. Set `WEBHOOK_SECRET_ENCRYPTION_KEY_PREVIOUS` to the current (soon-to-be-old) key, bump
   `WEBHOOK_SECRET_ENCRYPTION_KEY_VERSION` by one, set `WEBHOOK_SECRET_ENCRYPTION_KEY` to
   the new key, and deploy.
2. Run the backfill query against `POSTGRES_URL` (replace `:old_key`, `:new_key`,
   `:new_version` with the actual rotation values):
   ```sql
   UPDATE organization.webhook_endpoints
   SET secret_encrypted = pgp_sym_encrypt(pgp_sym_decrypt(secret_encrypted, :old_key), :new_key),
       secret_encrypted_previous = CASE WHEN secret_encrypted_previous IS NULL THEN NULL
           ELSE pgp_sym_encrypt(pgp_sym_decrypt(secret_encrypted_previous, :old_key), :new_key) END,
       key_version = :new_version
   WHERE key_version != :new_version;
   ```
3. Verify every row converged before proceeding:
   ```sql
   SELECT count(*) FROM organization.webhook_endpoints WHERE key_version != :new_version;
   ```
   Only proceed to step 4 once this returns `0`.
4. Clear `WEBHOOK_SECRET_ENCRYPTION_KEY_PREVIOUS` and redeploy.

**Stuck webhook-driven payment confirmation** — Any environment that ran the `FORCE ROW LEVEL SECURITY` migration before `stratum_webhook` (the dedicated `BYPASSRLS` role webhook processing now runs on) shipped may have invoices a customer actually paid for, where the provider's webhook silently failed to apply — the webhook routes carry no authenticated org context, so a role still bound by RLS can't see the rows it needs to update. Find candidates:
```sql
SELECT i.id, i.status, pl.external_id, pl.provider, s.subject_id
FROM billing.invoices i
JOIN billing.payment_links pl ON pl.invoice_id = i.id
JOIN billing.subscriptions s ON s.id = i.subscription_id
WHERE i.status = 'pending' AND pl.status = 'pending' AND i.created_at < now() - interval '1 hour';
```
Each result needs manual verification against the payment provider's own dashboard (Stripe/Xendit) before marking anything paid by hand via Studio's support tools — a row in this list only means the invoice looks stale, not that it was actually paid.

## Security incident response

**Severity.** *Critical*: confirmed exposure of customer data or credentials, or an attacker with access. *High*: a likely compromise under investigation, or a control confirmed bypassed. *Medium*: repeated attack signals with no sign of success.

**1. Contain.** Act before investigating further, and keep a timestamped log of every action.
- Compromised user: ban them in the Supabase dashboard, which stops sign-ins and token refresh. An access token already issued stays valid until it expires (`AUTH_ACCESS_TOKEN_MAX_TTL`).
- Compromised organization: suspend it from Studio's organization page.
- Exposed secret: rotate it (see "Secret rotation").
- Compromised operator: `ALTER ROLE op_<name> NOLOGIN`, and rotate the shared RabbitMQ and Redis credentials they held.

**2. Preserve evidence.** Before cleaning anything up, take a database snapshot (`make db-backup`), export the audit log as CSV from Studio's audit page, and copy the Postgres and application logs off the host. Retention sweeps keep running, so export anything close to its retention limit.

**3. Assess and notify.** Work out what data was affected and whose. Each deadline runs from the point you become aware or finish assessing, so involve legal counsel early:
- **GDPR Art. 33:** notify the supervisory authority within 72 hours of becoming aware of a personal data breach, unless it is unlikely to result in a risk to individuals. Art. 34: notify affected individuals when the risk is high.
- **Singapore PDPA:** notify the PDPC within 3 calendar days of assessing a breach as notifiable, and affected individuals where required.
- Customer contracts often add their own notification terms.

**4. Recover.** Fix the root cause, restore from backup if data was altered (`make db-restore-verify` first), and watch the alert catalog for a recurrence.

**5. Review.** Within two weeks, write a post-incident review: timeline, root cause, what worked, and follow-up actions with owners. Keep it as audit evidence.

## Operator access
Studio changes are attributed through one Postgres login per person. Never share a login.

**Provision**, with a superuser-equivalent connection. `VALID UNTIL` at most 100 days out; `BYPASSRLS` is set on the login because Postgres does not inherit it through membership, and Studio refuses a login without it:
```sql
CREATE ROLE op_<name> LOGIN BYPASSRLS PASSWORD '<generated>' VALID UNTIL '<date within 100 days>' IN ROLE stratum_operator;
```
Then put that DSN in the project's Studio form. Editing a project reconnects it immediately.

**Review every quarter** and keep the output as evidence. For each operator, either renew `VALID UNTIL` or set `NOLOGIN`:
```sql
SELECT login.rolname,
       login.rolcanlogin,
       login.rolvaliduntil,
       (SELECT max(e.created_at) FROM audit.events e WHERE e.actor = 'operator:' || login.rolname) AS last_change
FROM pg_auth_members m
JOIN pg_roles operator_group ON operator_group.oid = m.roleid
JOIN pg_roles login ON login.oid = m.member
WHERE operator_group.rolname = 'stratum_operator'
ORDER BY login.rolname;
```

**Revoke** with `ALTER ROLE op_<name> NOLOGIN`. Never `DROP` it, so its audit history stays attributable to a role that still exists.

**Break-glass.** The schema owner is the emergency path. The audit trigger does not capture owner or superuser sessions, so `ALTER ROLE <owner> SET log_statement = 'mod'` makes Postgres log every data-modifying statement the owner runs, and that log must be shipped off-host. Record why each break-glass session happened.

## Secret rotation
Rotate on a schedule, and immediately after any suspected exposure or when someone with access leaves. Restart the API and worker after changing their environment.

| Secret | How to rotate |
|---|---|
| `STRIPE_API_KEY`, `STRIPE_WEBHOOK_SECRET` | Roll the key, or add a new webhook endpoint secret, in the Stripe dashboard, then update the environment. Stripe can keep the old secret valid for a window, so nothing is missed |
| `XENDIT_API_KEY`, `XENDIT_CALLBACK_TOKEN` | Generate new values in the Xendit dashboard and update the environment in the same deploy, then check Xendit's callback log for anything rejected in between |
| `AUTH_SERVICE_ROLE_KEY`, `SUPABASE_WEBHOOK_SECRET` | Roll in Supabase, then update the environment and the database webhook's header together |
| `STATS_TOKEN` | Change the environment and the value in Studio's project form |
| Database role passwords | `ALTER ROLE <role> PASSWORD '<generated>'`, then update that role's `POSTGRES_*_URL`. Existing connections stay open until restart |
| RabbitMQ and Redis credentials | Change them in the broker or cache, then update the environment and every Studio project form |
| `WEBHOOK_SECRET_ENCRYPTION_KEY` | Follow "Rotating the webhook encryption key" above |

## No scheduler
Stratum has no cron. Anything that looks periodic is one of:

- **A future-dated outbox row.** Dunning notices, renewal reminders, and the end-of-period expiry check are written to `messaging.outbox` with a future `not_before`; the relay claims each only once it comes due and publishes it like any immediate event. The delay is a timestamp in the database, not a broker plugin or a scheduler.
- **A worker-local ticker.** `cmd/worker` runs a handful of `time.Ticker` goroutines in-process — the outbox relay (2s), the outbox and module retention sweeps (hourly; module sweeps remove 10,000 rows at a time until none are left and log what they removed), expired-invitation cleanup (hourly), and `ReconcileOverdueSubscriptions` (hourly), which re-enqueues an expiry check for any active/trialing subscription already past its end date so a lost scheduled check can't leave a lapsed subscription entitled. These are not external jobs; they stop when the worker stops.
- **Lazy/opportunistic work** — e.g. webhook health computed on delivery.

Adding a general-purpose scheduler still isn't judged worth the operational surface.
