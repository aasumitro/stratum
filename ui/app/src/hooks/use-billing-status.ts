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
  const hasPendingInvoice =
    isOwner &&
    (invoicesData?.data ?? []).some(
      (i) => i.status === "pending" && i.kind !== "extension"
    )

  return {
    isOwner,
    isSuspended,
    hasPendingInvoice,
    isBillingBlocked: hasPendingInvoice || isSuspended,
  }
}
