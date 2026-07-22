import { createFileRoute } from "@tanstack/react-router"
import { BillingPaymentSuccessPage } from "@/features/billing/pages/billing-payment-success-page"

export const Route = createFileRoute("/billing/success")({
  component: BillingPaymentSuccessPage,
})
