import { createFileRoute } from "@tanstack/react-router"
import { BillingHistoryPage } from "@/features/billing/pages/billing-history-page"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/billing/history"
)({
  component: BillingHistoryPage,
})
