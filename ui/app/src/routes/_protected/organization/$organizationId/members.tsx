import { createFileRoute } from "@tanstack/react-router"
import { MembersPage } from "@/features/organization/pages/members-page"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/members"
)({
  component: MembersPage,
})
