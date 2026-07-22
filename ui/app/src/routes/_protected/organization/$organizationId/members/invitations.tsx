import { createFileRoute } from "@tanstack/react-router"
import { MembersInvitationsPage } from "@/features/organization/pages/members-invitations-page"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/members/invitations"
)({
  component: MembersInvitationsPage,
})
