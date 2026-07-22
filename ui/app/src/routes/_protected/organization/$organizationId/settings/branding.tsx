import { createFileRoute } from "@tanstack/react-router"
import { OrganizationSettingsBrandingPage } from "@/features/organization/pages/organization-settings-branding-page"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/settings/branding"
)({
  component: OrganizationSettingsBrandingPage,
})
