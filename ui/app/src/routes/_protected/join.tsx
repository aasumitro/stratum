import { createFileRoute } from "@tanstack/react-router"
import { JoinOrganizationPage } from "@/features/organization/pages/join-organization-page"

export const Route = createFileRoute("/_protected/join")({
  component: JoinOrganizationPage,
})
