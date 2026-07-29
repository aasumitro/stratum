import { createFileRoute } from "@tanstack/react-router"
import { OrganizationLayout } from "@/features/organization/pages/organization-layout"
import { NotFoundPage } from "@/components/shared/not-found-page"
import { RouteErrorBoundary } from "@/components/shared/route-error-boundary"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId"
)({
  component: OrganizationLayout,
  notFoundComponent: NotFoundPage,
  errorComponent: RouteErrorBoundary,
})
