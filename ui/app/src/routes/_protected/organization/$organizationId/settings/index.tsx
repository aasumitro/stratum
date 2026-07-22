import { createFileRoute } from "@tanstack/react-router"
import { OrganizationSettingsGeneralPage } from "@/features/organization/pages/organization-settings-general-page"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/settings/"
)({
  component: OrganizationSettingsGeneralPage,
})
