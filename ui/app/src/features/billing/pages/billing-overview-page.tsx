import { useParams } from "@tanstack/react-router"
import { SubscriptionCard } from "@/features/billing/components/subscription-card"
import { UsageMeters } from "@/features/billing/components/usage-meters"
import { FeaturesSection } from "@/features/billing/components/features-section"
import { AddonsSection } from "@/features/billing/components/addons-section"

// The Plan tab — everything about the org's current plan (status, usage,
// entitlements, add-ons) on one page, none of it a paginated table, so no
// sub-tabs. Coupon redemption stays in the Pay dialog, the only place a
// coupon is actually applied against a real charge.
export function BillingOverviewPage() {
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }
  return (
    <div className="flex flex-col gap-4">
      <SubscriptionCard organizationId={organizationId} />
      <UsageMeters organizationId={organizationId} />
      <FeaturesSection organizationId={organizationId} />
      <AddonsSection organizationId={organizationId} />
    </div>
  )
}
