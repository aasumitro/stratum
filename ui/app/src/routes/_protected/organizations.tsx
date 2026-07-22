import { createFileRoute } from "@tanstack/react-router"
import { OrganizationPickerPage } from "@/features/organization/pages/organization-picker-page"

export const Route = createFileRoute("/_protected/organizations")({
  component: OrganizationPickerPage,
})
