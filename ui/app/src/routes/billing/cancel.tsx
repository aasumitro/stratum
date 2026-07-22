import { createFileRoute } from "@tanstack/react-router"
import { BillingPaymentCancelPage } from "@/features/billing/pages/billing-payment-cancel-page"

export const Route = createFileRoute("/billing/cancel")({
  component: BillingPaymentCancelPage,
})
