import { createFileRoute } from "@tanstack/react-router"
import { OrganizationLayout } from "@/features/organization/pages/organization-layout"
import { NotFoundPage } from "@/components/shared/not-found-page"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId"
)({
  component: OrganizationLayout,
  notFoundComponent: NotFoundPage,
})
