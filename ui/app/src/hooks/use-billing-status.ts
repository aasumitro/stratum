import { useActiveOrganization } from "@/hooks/use-active-organization"
import { useInvoices } from "@/features/billing/hooks"

interface BillingStatus {
  isOwner: boolean
  isSuspended: boolean
  hasPendingInvoice: boolean
  /** an unpaid activation invoice — the period in use was never paid */
  hasBlockingInvoice: boolean
  /** blocking invoice or suspended — gates Members/Webhooks/Files nav + redirects */
  isBillingBlocked: boolean
}

// The suspension reasons billing itself writes when a subscription ends
// (expired unpaid, or cancelled once the cancellation took effect). Paying
// lifts them; an owner's self-unsuspend never does.
const BILLING_SUSPENSION_REASONS = new Set([
  "subscription expired",
  "subscription cancelled",
])

export function isBillingSuspensionReason(reason?: string): boolean {
  return reason !== undefined && BILLING_SUSPENSION_REASONS.has(reason)
}

// The reason the owner's own pause stores — the only suspension the owner
// can lift from the app. A reason that is neither this nor a billing one
// was set by support (Studio), which lifts it there.
const SELF_SUSPENSION_REASON = "Suspended by organization owner"

export function isSelfSuspensionReason(reason?: string): boolean {
  return reason === SELF_SUSPENSION_REASON
}

/**
 * Single source for billing-blocked state.
 */
export function useBillingStatus(): BillingStatus {
  const { organizationId, organization, role } = useActiveOrganization()
  const isOwner = role === "owner"
  const isSuspended = organization?.status === "suspended"

  const { data: invoicesData } = useInvoices(
    organizationId ?? "",
    !!organizationId && isOwner
  )
  const pending = (invoicesData?.data ?? []).filter(
    (i) => i.status === "pending"
  )
  // extension and addon_increase invoices both pay for something additional
  // to the current period (more time, more capacity) — the plan and
  // capacity you already have stay exactly as valid as before either is
  // paid, so neither counts here. Only an invoice that backs a period itself
  // (subscription/activation) does.
  const hasPendingInvoice =
    isOwner &&
    pending.some((i) => i.kind !== "extension" && i.kind !== "addon_increase")
  // Only an unpaid activation means the period in use right now was never
  // paid. A renewal invoice is issued 3 days before a paid period ends, so
  // it shows on the billing page but must not lock the owner out of the
  // period they already have.
  const hasBlockingInvoice =
    isOwner && pending.some((i) => i.kind === "activation")

  return {
    isOwner,
    isSuspended,
    hasPendingInvoice,
    hasBlockingInvoice,
    isBillingBlocked: hasBlockingInvoice || isSuspended,
  }
}
