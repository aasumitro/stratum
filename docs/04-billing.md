# 04 · Billing

Billing is Stratum's license engine — it decides what an organization can do and for how long. It handles record-keeping and lifecycle; the actual money movement is delegated to payment gateways.

## Subscription lifecycle

```
trialing → active → cancelled → expired
active ⇄ past_due
```

When someone creates their **first** organization, it gets a 7-day trial with no invoice. Every **subsequent** organization they own goes straight to `active` with an invoice created on the spot. The creator must choose a plan and billing cycle at creation time (both validated against the live catalog), and can optionally add addons and a coupon code to the same request — all validated up front and, for a 2nd+ org, already priced into that first invoice. A plan with no price is arranged with sales and cannot be picked (`422 PLAN_NOT_SELF_SERVICE`). Joining an existing organization never involves a plan choice.

Owners can **change plan** (with proration), **cancel** (with a required reason), **resume**, **extend** (buy 1–24 more months, capped at a 2-year total lifetime), and **activate** (skip the rest of a trial and invoice a full cycle now — the subscription becomes active immediately, but that invoice still has to be paid like any other).

While an organization's first invoice (a 2nd+ organization's, or activate-now's) is unpaid, the period in use was never paid for, so the API refuses writes for every member and every client with `403 ACTIVATION_UNPAID`. Reading stays open, and so do Billing, deleting the organization, and leaving it. Paying lifts it on the next request. Locally, with no payment provider configured, Studio's Mark paid settles the invoice.

A cancel never cuts access early. On every status, a trial included, it is *scheduled*: the subscription keeps its status and full access until the paid period (or the trial) ends, the owner can undo it until then, and a renewal invoice already issued for the next period is voided. When the period ends, the cancellation takes effect: the subscription becomes `cancelled` and the organization is suspended. While a cancellation is scheduled, a downgrade, an add-on decrease and activate-now are refused until it is undone.

Resume (from `cancelled` or `expired`) always goes through payment: it issues an invoice for the next period and leaves the status as it is; paying that invoice reactivates the subscription with a fresh period and lifts the suspension. Asking again while that invoice is unpaid returns the same invoice.

A downgrade or an add-on decrease on a paid subscription is *scheduled* for the end of the current period (`scheduled_effective_at` on the subscription). The renewal invoice for the next period is priced at the scheduled plan, cycle and quantities, and is reissued whenever the schedule changes while it is unpaid. The change itself applies at that boundary, once the renewal is paid; until then the current plan and its limits stay. Members over the new limit are resolved then. The owner can undo or change a schedule until the renewal priced from it is paid (`422 AMENDMENT_ALREADY_PAID` afterwards; a plan change or an Extend is refused in that window too). An extension paid before that moves the boundary to the extended end. A subscription that expires unpaid at the boundary still takes the schedule, so a later Resume is priced and runs on the chosen plan. On an expired or cancelled subscription a downgrade applies at once, since no period is running. Any applied plan change replaces a scheduled downgrade.

A plan change never reactivates anything on its own. It prorates the remaining paid time on an active or past-due subscription, which can shorten the period but never lengthen it. While the first (activation) invoice is still unpaid there is no paid time to prorate: the change reissues that invoice at the new price, under the same rule. Paying the invoice then grants one full cycle from the period's start. A plan change goes upward or sideways: a lower plan is refused on every status (`422 PLAN_CHANGE_IS_DOWNGRADE`) because it goes through the downgrade flow, which resolves the members over its limit, and a plan with no price is refused (`422 PLAN_NOT_SELF_SERVICE`) unless the subscription is already on it and only the cycle changes.

Downgrading to a plan below current usage doesn't silently grandfather the organization over its new limit: the owner picks specific members to remove as part of the same request, with deterministic auto-fill (newest-joined members) covering anything left unselected — a capacity add-on already attached to the subscription is accounted for, and the owner can never be removed. What actually got removed, and how much of it was auto-selected vs. owner-chosen, is recorded and returned in the response.

Extending is priced in tiered blocks, not flat months: every full 12-month block bills at the plan's yearly rate, with any leftover months at the monthly rate. Extending by exactly 12 months can instead switch the subscription's billing cycle to yearly going forward ("Switch to Annual") for the same price as a plain 12-month extend.

## The catalog

Plans, features, add-ons, and coupons are a real, enforced catalog in the database — not hardcoded. 

- **Plans** carry per-currency prices and a sort order.
- **Features** have a type: *metered* (tracked usage like members), *boolean* (an on/off gate), *static* (display-only), or *config* (a JSON value delivered to the app, e.g. the API rate limit).
- **Add-ons** attach extra numeric capacity on top of a plan (e.g. +10 members). On a trial, any change applies immediately. On a paid subscription, decreasing (or removing) an add-on takes effect at the next renewal rather than shrinking your capacity mid-period; increasing one — including attaching it for the first time — creates a day-prorated invoice on the spot, and the higher limit only takes effect once that invoice is paid. An increase is refused while the first invoice is unpaid, and on an ended subscription while a Resume invoice is pending (`422 PERIOD_INVOICE_PENDING`). Paying an increase reissues a pending renewal at the new quantity and drops a pending extension, since both were priced from the old one. On a trial, any change reissues the invoice converting the trial.
- **Coupons** apply fixed or percentage discounts with a once/repeated/forever cadence and optional targeting.

`GET /billing/features` returns the *resolved* entitlements for an organization — one row per feature it's granted, with metered current/limit/remaining and the plan-vs-add-on split.

A single code path composes every invoice: plan price + attached add-ons − any active coupon discount. Coupons are **subscription-level** — applying one discounts your *next* invoice, not one that already exists. A voided or failed invoice gives its coupon application back, so what replaces it keeps a once-only discount, and a code can be redeemed once per subscription. A fixed-amount coupon does not discount an add-on increase, because an increase can be split into as many small invoices as the owner likes; a percentage coupon does, and is used by it.

A coupon can cover a charge in full. There is then no invoice, because an invoice must have an amount to pay: the coupon is used and the charge has the effect a payment would, so a renewal rolls the period and a Resume reactivates at once. A plan with no price and no coupon is different, since nobody has paid for it: activate-now and Resume are refused with `422 PLAN_NOT_SELF_SERVICE`, and Extend with `422 SUBSCRIPTION_NOT_EXTENDABLE`.

An invoice does not outlive what it was priced from. Activate-now replaces an invoice already issued to convert the trial. A decrease or removal drops that add-on's pending increase. A paid renewal or extension moves the period, so the other one and every pending add-on increase are voided: an increase was priced for the days left in the old period, and paying it afterwards would carry the new quantity through the new one. The owner requests a voided extension or increase again, at the right price.

## Payments

Stripe handles USD, Xendit handles IDR (including local methods like QRIS, e-wallets, and bank transfer via hosted checkout). Stratum generates a payment link per invoice (idempotent, regenerated on expiry) and then waits. Subscription and invoice state advance **only** when a signed, verified webhook arrives from the gateway — a client can never move billing state directly.

```
invoice created → payment link → customer pays on the gateway's page → webhook
  → signature verified → subscription/invoice updated → notification + audit fired
```

There is no autopay and no stored card data by design — renewal is always a manual hosted-checkout payment.

A checkout the owner leaves unpaid until it expires (Stripe's `checkout.session.expired`, Xendit's
`EXPIRED`) is not a failed payment: only its link closes. The invoice stays payable, Pay opens a new
checkout, and nothing else changes: no failure notice, no dunning, no `billing.invoice.failed`, no
`past_due`. An invoice still unpaid when its period or trial ends is closed by the expiry, as for any
unpaid invoice. Only a payment attempt that failed (a declined card, a bank debit that bounced,
Xendit's `FAILED`) notifies the owner, starts dunning and, for a renewal or activation, moves the
subscription to `past_due`.

When an invoice is voided or failed, or a new payment link replaces an old one, the old checkout is
closed at Stripe/Xendit as well (the worker calls the provider's expire endpoint), so it can no
longer take money. If a payment still lands on such an invoice — a checkout completed just before
it closed — and the subscription has already ended, the payment reactivates the subscription like
a Resume payment, unless the plan, the cycle or an add-on grew after the invoice was issued, since
that payment did not buy them. Those, a payment on a live subscription whose replacement invoice
is already pending, and a second payment of an invoice already paid are recorded as payments against
the invoice they paid and applied to nothing. Studio lists them under Support for an operator to
refund at the provider; there is no refund in the app. A paid reactivation lifts only billing's own suspension,
never an owner's pause or a support suspension.

## Invoices

Invoices get sequential per-organization numbers and are rendered to a brand-styled PDF on the fly (bilingual, `?lang=en|id`). Tax is applied per country in basis points at generation time; currency is derived from the organization's country.

## Usage and quotas

One metric is tracked today — `members` — recorded automatically (and fire-and-forget) after each relevant action. The current count is the one recorded last; an upgrade moves `period_end` earlier, so the row with the latest end can be older. Before an invite, add or join, current usage is compared to the effective quota (plan limit + any add-on deltas); a request at the limit is refused with HTTP 422 `PLAN_LIMIT_REACHED`. A one-time warning fires when usage crosses 90% of a metered limit.

## Renewal and dunning

Renewal reminders and overdue-payment (dunning) notices are scheduled as transactional-outbox rows with a future `not_before` — a day-3 reminder and a day-7 final notice — which the relay publishes once they come due. Billing checks each dunning notice against the live invoice when it comes due and sends it only while it is still true: the invoice is still unpaid, it pays for the subscription's own period (not an add-on or extension), and the subscription has not ended. A renewal payment completed after the subscription already expired — a late webhook, or a hosted checkout the customer finished after the expiry check — is recovered: the invoice is marked paid and the subscription reactivated with a fresh period. Trials use a shorter 2-day lead, and trial-specific email copy makes clear the reader is on a trial rather than a paid renewal. An hourly `ReconcileOverdueSubscriptions` sweep in the worker backstops expiry: it re-enqueues an expiry check for any active or trialing subscription already past its end date, so a lost scheduled check still results in the subscription being expired and its organization suspended.
