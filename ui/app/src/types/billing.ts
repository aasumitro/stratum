import type { PlanPrices } from "@/types/reference"

export type SubscriptionStatus =
  "trialing" | "active" | "cancelled" | "past_due" | "expired"
export type BillingCycle = "monthly" | "yearly"
export type CancelReason =
  | "too_expensive"
  | "missing_features"
  | "switching_provider"
  | "no_longer_needed"
  | "other"
export type InvoiceStatus = "pending" | "paid" | "failed" | "void"
export type HistoryAction =
  | "trial"
  | "activate"
  | "upgrade"
  | "downgrade"
  | "cancel"
  | "resume"
  | "expire"
  | "extend"
  | "addon_change"

export interface Subscription {
  id: string
  subject_type: string
  subject_id: string
  // Real FK to billing.plans(id).
  plan: string
  status: SubscriptionStatus
  cycle: BillingCycle
  currency: string
  period_start?: string
  period_end?: string
  trial_end?: string
  active_coupon?: string
  // How many more months this subscription can be extended by before
  // hitting its 24-month lifetime cap — backend-computed (same arithmetic
  // the extend endpoint itself enforces) so the frontend never re-derives
  // it from period_end/created_at and risks disagreeing by a day.
  max_extendable_months: number
  // A plan downgrade or cancellation on a non-trialing subscription defers
  // to renewal instead of applying immediately — these three fields are set
  // while an amendment is scheduled and clear once the renewal worker
  // applies (or the owner undoes) it.
  scheduled_plan?: string
  scheduled_cycle?: BillingCycle
  scheduled_cancel_at?: string
  created_at: string
  updated_at: string
}

export interface Invoice {
  id: string
  subscription_id: string
  invoice_number?: string
  amount_cents: number
  subtotal_cents?: number
  tax_rate_bps: number
  tax_cents: number
  currency: string
  status: InvoiceStatus
  kind: "subscription" | "extension" | "activation" | "addon_increase"
  // Only meaningful on an "extension" invoice — whether paying it also
  // converts the subscription's cycle to yearly (applied by the backend on
  // payment confirmation, not when the invoice is created).
  switch_to_annual: boolean
  provider_invoice_id?: string
  due_at?: string
  paid_at?: string
  created_at: string
  updated_at: string
}

export interface PaymentLink {
  id: string
  invoice_id: string
  provider: string
  currency: string
  amount_cents: number
  external_id?: string
  url?: string
  status: string
  expires_at?: string
  created_at: string
}

export interface Payment {
  id: string
  invoice_id: string
  amount_cents: number
  currency: string
  provider: string
  external_id?: string
  status: string
  paid_at: string
  created_at: string
}

export interface SubscriptionHistory {
  id: string
  subscription_id: string
  action: HistoryAction
  from_plan?: string
  to_plan?: string
  amount_cents: number
  currency: string
  changed_by: string
  // changed_by resolved to a display name + actor kind. "user" = a real
  // auth_sub, resolved to their name/email when resolvable; "system"/
  // "webhook" render as a chip, not an avatar.
  changed_by_name: string
  changed_by_kind: "user" | "system" | "webhook"
  changed_at: string
  metadata?: unknown
  // phase distinguishes a scheduled-but-not-yet-applied change from one
  // that already took effect, or one that was scheduled and then undone
  // before it ever applied — undefined for an action with no phase concept
  // (upgrade, extend, activate, resume, expire, immediate/trial addon change).
  phase?: "scheduled" | "applied" | "undone"
  // effective_at is when a scheduled/applied/undone row took (or will take)
  // effect — distinct from changed_at, which is always when the row itself
  // was written (e.g. the moment a downgrade was scheduled, not when it
  // applies at renewal).
  effective_at?: string
  from_cycle?: BillingCycle
  to_cycle?: BillingCycle
}

export interface UsageMetric {
  id: string
  organization_id: string
  metric: string
  value: number
  period_start: string
  period_end: string
  recorded_at: string
}

// Entitlement is the resolved per-feature view returned by
// GET /organizations/:id/billing/features. limit/current/remaining only apply to
// type="metered" (limit=-1 means unlimited); config_value only applies to
// type="config".
export interface Entitlement {
  feature_id: string
  name: string
  type: "metered" | "boolean" | "static" | "config"
  limit?: number
  // plan_limit/addon_delta split out of limit (limit = plan_limit +
  // addon_delta) — backs the usage bar's "6 / 8 · 3 plan + 5 addon"
  // annotation.
  plan_limit?: number
  addon_delta?: number
  current?: number
  remaining?: number
  config_value?: unknown
}

export interface AttachedAddon {
  addon_id: string
  name: string
  quantity: number
  prices: Record<string, PlanPrices>
  // Set while a quantity decrease (or removal, quantity 0) is scheduled for
  // renewal instead of applied immediately; both clear together once applied
  // or undone.
  scheduled_quantity?: number
  scheduled_requested_at?: string
  // Set while an increase (or a brand-new attach) is awaiting payment — the
  // opposite direction from scheduled_quantity: quantity only rises to
  // pending_quantity once pending_invoice_id's invoice is confirmed paid.
  // Both clear together once applied or superseded by a newer request.
  pending_quantity?: number
  pending_invoice_id?: string
}

// Coupon is the shape returned by GET .../billing/coupons (eligible-coupon
// list) — never auto-applied, always an explicit Apply in the Pay dialog.
export interface Coupon {
  code: string
  name: string
  discount_type: "fixed" | "percent"
  amount_cents?: number
  percent_off?: number
  currency?: string
  cadence: "once" | "repeated" | "forever"
  duration_count?: number
  valid_from?: string
  valid_until?: string
  max_redemptions?: number
  redeemed_count: number
  active: boolean
}

export interface InvoicePreviewLine {
  description: string
  quantity: number
  unit_price_cents: number
  total_cents: number
}

// InvoicePreview is a read-only composition — GET .../billing/preview with
// no params previews the subscription's current state (backs the
// pending-changes bar); with ?plan=&cycle= it previews a hypothetical
// change (backs the change-plan dialog's proration text).
export interface InvoicePreview {
  plan: string
  cycle: BillingCycle
  currency: string
  plan_line_cents: number
  addon_lines?: InvoicePreviewLine[]
  coupon_code?: string
  discount_cents?: number
  total_cents: number
  new_period_end?: string
  overage?: {
    members?: {
      current: number
      allowed: number
      auto_select_removals: string[]
    }
  }
}

// OverageResolution mirrors contracts.OverageResolution — what
// POST .../billing/downgrade actually removed, split by whether each item
// was the owner's manual pick or the deterministic auto-fill. Distinct from
// InvoicePreview's `overage` field above: that one is a dry-run computed
// with no manual selection, this one is what really happened.
export interface OverageResolution {
  removed_member_auth_subs: string[]
  auto_selected_member_subs: string[]
}

export interface DowngradeResult {
  subscription: Subscription
  overage: OverageResolution
}
