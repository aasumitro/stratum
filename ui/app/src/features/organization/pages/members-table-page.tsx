import { useParams } from "@tanstack/react-router"
import { useOrganizations } from "@/features/organization/hooks"
import { MembersTable } from "@/features/organization/components/members-table"
import type { OrganizationRole } from "@/types/organization"

export function MembersTablePage() {
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }
  const { data: listData } = useOrganizations()
  const role = listData?.data?.find((w) => w.id === organizationId)?.role as
    | OrganizationRole
    | undefined

  return <MembersTable organizationId={organizationId} currentRole={role} />
}
