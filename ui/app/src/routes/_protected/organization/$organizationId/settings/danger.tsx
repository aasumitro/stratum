import { createFileRoute } from "@tanstack/react-router"
import { OrganizationSettingsDangerPage } from "@/features/organization/pages/organization-settings-danger-page"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/settings/danger"
)({
  component: OrganizationSettingsDangerPage,
})
