import { useParams } from "@tanstack/react-router"
import { useOrganizations } from "@/features/organization/hooks"
import { InvitationsTable } from "@/features/organization/components/invitations-table"
import type { OrganizationRole } from "@/types/organization"

export function MembersInvitationsPage() {
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }
  const { data: listData } = useOrganizations()
  const role = listData?.data?.find((w) => w.id === organizationId)?.role as
    | OrganizationRole
    | undefined
  const canManage = role === "owner" || role === "admin"

  return (
    <InvitationsTable organizationId={organizationId} canManage={canManage} />
  )
}
