import { useActiveOrganization } from "@/hooks/use-active-organization"
import { useInvoices } from "@/features/billing/hooks"

interface BillingStatus {
  isOwner: boolean
  isSuspended: boolean
  hasPendingInvoice: boolean
  /** pending invoice or suspended — gates Members/Webhooks/Files nav + redirects */
  isBillingBlocked: boolean
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
  // extension and addon_increase invoices both pay for something additional
  // to the current period (more time, more capacity) — the plan and
  // capacity you already have stay exactly as valid as before either is
  // paid, so neither should block access to it. Only an invoice that backs
  // the current period itself (subscription/activation) does that.
  const hasPendingInvoice =
    isOwner &&
    (invoicesData?.data ?? []).some(
      (i) =>
        i.status === "pending" &&
        i.kind !== "extension" &&
        i.kind !== "addon_increase"
    )

  return {
    isOwner,
    isSuspended,
    hasPendingInvoice,
    isBillingBlocked: hasPendingInvoice || isSuspended,
  }
}
