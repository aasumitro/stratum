import { useParams } from "@tanstack/react-router"
import { SubscriptionCard } from "@/features/billing/components/subscription-card"
import { UsageMeters } from "@/features/billing/components/usage-meters"
import { InvoicesTable } from "@/features/billing/components/invoices-table"
import { AddonsSection } from "@/features/billing/components/addons-section"

// The whole of billing on one page
// — status, usage, invoice history, and add-ons, none of it needing a
// paginated route of its own. Coupon redemption stays in the Pay dialog,
// the only place a coupon is actually applied against a real charge.
export function BillingOverviewPage() {
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }
  return (
    <div className="flex flex-col gap-4">
      <SubscriptionCard organizationId={organizationId} />
      <div className="grid gap-4 lg:grid-cols-2">
        <UsageMeters organizationId={organizationId} />
        <AddonsSection organizationId={organizationId} />
      </div>
      <InvoicesTable organizationId={organizationId} />
    </div>
  )
}
