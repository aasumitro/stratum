import { createFileRoute } from "@tanstack/react-router"
import { InvitationAcceptPage } from "@/features/organization/pages/invitation-accept-page"

export const Route = createFileRoute("/_protected/invitations/accept")({
  component: InvitationAcceptPage,
})
