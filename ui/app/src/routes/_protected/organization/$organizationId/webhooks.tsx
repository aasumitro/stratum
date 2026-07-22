import { createFileRoute } from "@tanstack/react-router"
import { WebhooksPage } from "@/features/organization/pages/webhooks-page"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/webhooks"
)({
  component: WebhooksPage,
})
