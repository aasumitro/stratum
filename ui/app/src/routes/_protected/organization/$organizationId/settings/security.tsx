import { createFileRoute } from "@tanstack/react-router"
import { OrganizationSettingsSecurityPage } from "@/features/organization/pages/organization-settings-security-page"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/settings/security"
)({
  component: OrganizationSettingsSecurityPage,
})
