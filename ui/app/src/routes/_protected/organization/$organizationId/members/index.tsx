import { createFileRoute } from "@tanstack/react-router"
import { MembersTablePage } from "@/features/organization/pages/members-table-page"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/members/"
)({
  component: MembersTablePage,
})
