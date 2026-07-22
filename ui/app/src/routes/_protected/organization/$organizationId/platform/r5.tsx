import { createFileRoute } from "@tanstack/react-router"
import { ReservedPage } from "@/components/shared/reserved-page"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/platform/r5"
)({
  component: () => <ReservedPage label="Reserve5" />,
})
