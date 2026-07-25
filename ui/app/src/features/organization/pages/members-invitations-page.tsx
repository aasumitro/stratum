import { useParams } from "@tanstack/react-router"
import { usePermissions } from "@/hooks/use-permissions"
import { InvitationsTable } from "@/features/organization/components/invitations-table"

export function MembersInvitationsPage() {
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }
  const { canManageMembers: canManage } = usePermissions()

  return (
    <InvitationsTable organizationId={organizationId} canManage={canManage} />
  )
}
