import { createFileRoute } from "@tanstack/react-router"
import { OrganizationSettingsPage } from "@/features/organization/pages/organization-settings-page"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/settings"
)({
  component: OrganizationSettingsPage,
})
