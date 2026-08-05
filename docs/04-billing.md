# 04 · Billing

Billing is Stratum's license engine — it decides what an organization can do and for how long. It handles record-keeping and lifecycle; the actual money movement is delegated to payment gateways.

## Subscription lifecycle

```
trialing → active → cancelled → expired
active ⇄ past_due
```

When someone creates their **first** organization, it gets a 7-day trial with no invoice. Every **subsequent** organization they own goes straight to `active` with an invoice created on the spot. The creator must choose a plan and billing cycle at creation time (both validated against the live catalog), and can optionally add addons and a coupon code to the same request — all validated up front and, for a 2nd+ org, already priced into that first invoice. Joining an existing organization never involves a plan choice.

Owners can **change plan** (with proration), **cancel** (access continues until period end, with a required reason), **resume**, **extend** (buy 1–24 more months, capped at a 2-year total lifetime), and **activate** (skip the rest of a trial and invoice a full cycle now — the subscription becomes active immediately, but that invoice still has to be paid like any other). One subtlety worth knowing: resuming a subscription that was cancelled *while still in its trial* correctly returns it to `trialing`, not `active`.

Downgrading to a plan below current usage doesn't silently grandfather the organization over its new limit: the owner picks specific members to remove as part of the same request, with deterministic auto-fill (newest-joined members) covering anything left unselected — a capacity add-on already attached to the subscription is accounted for, and the owner can never be removed. What actually got removed, and how much of it was auto-selected vs. owner-chosen, is recorded and returned in the response.

Extending is priced in tiered blocks, not flat months: every full 12-month block bills at the plan's yearly rate, with any leftover months at the monthly rate. Extending by exactly 12 months can instead switch the subscription's billing cycle to yearly going forward ("Switch to Annual") for the same price as a plain 12-month extend.

## The catalog

Plans, features, add-ons, and coupons are a real, enforced catalog in the database — not hardcoded. 

- **Plans** carry per-currency prices and a sort order.
- **Features** have a type: *metered* (tracked usage like members), *boolean* (an on/off gate), *static* (display-only), or *config* (a JSON value delivered to the app, e.g. the API rate limit).
- **Add-ons** attach extra numeric capacity on top of a plan (e.g. +10 members). On a trial, any change applies immediately. On a paid subscription, decreasing (or removing) an add-on takes effect at the next renewal rather than shrinking your capacity mid-period; increasing one — including attaching it for the first time — creates a day-prorated invoice on the spot, and the higher limit only takes effect once that invoice is paid.
- **Coupons** apply fixed or percentage discounts with a once/repeated/forever cadence and optional targeting.

`GET /billing/features` returns the *resolved* entitlements for an organization — one row per feature it's granted, with metered current/limit/remaining and the plan-vs-add-on split.

A single code path composes every invoice: plan price + attached add-ons − any active coupon discount. Coupons are **subscription-level** — applying one discounts your *next* invoice, not one that already exists.

## Payments

Stripe handles USD, Xendit handles IDR (including local methods like QRIS, e-wallets, and bank transfer via hosted checkout). Stratum generates a payment link per invoice (idempotent, regenerated on expiry) and then waits. Subscription and invoice state advance **only** when a signed, verified webhook arrives from the gateway — a client can never move billing state directly.

```
invoice created → payment link → customer pays on the gateway's page → webhook
  → signature verified → subscription/invoice updated → notification + audit fired
```

There is no autopay and no stored card data by design — renewal is always a manual hosted-checkout payment.

## Invoices

Invoices get sequential per-organization numbers and are rendered to a brand-styled PDF on the fly (bilingual, `?lang=en|id`). Tax is applied per country in basis points at generation time; currency is derived from the organization's country.

## Usage and quotas

One metric is tracked today — `members` — recorded automatically (and fire-and-forget) after each relevant action. Before a billable action, current usage is compared to the effective quota (plan limit + any add-on deltas); over-limit requests get an HTTP 429 with `X-Usage-Current` / `X-Usage-Limit` headers. A one-time warning fires when usage crosses 90% of a metered limit.

## Renewal and dunning

Renewal reminders and overdue-payment (dunning) notices are scheduled with RabbitMQ delayed messages — a day-3 reminder and a day-7 final notice. Trials use a shorter 2-day lead, and trial-specific email copy makes clear the reader is on a trial rather than a paid renewal.
