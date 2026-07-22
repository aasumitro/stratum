import { createFileRoute } from "@tanstack/react-router"
import { BillingLayout } from "@/features/billing/pages/billing-layout"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/billing"
)({
  component: BillingLayout,
})
