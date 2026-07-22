import { createFileRoute } from "@tanstack/react-router"
import { BillingOverviewPage } from "@/features/billing/pages/billing-overview-page"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/billing/"
)({
  component: BillingOverviewPage,
})
