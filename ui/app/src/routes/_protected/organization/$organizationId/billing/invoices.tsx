import { createFileRoute } from "@tanstack/react-router"
import { BillingInvoicesPage } from "@/features/billing/pages/billing-invoices-page"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/billing/invoices"
)({
  component: BillingInvoicesPage,
})
