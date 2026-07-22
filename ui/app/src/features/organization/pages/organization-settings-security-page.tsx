import { useParams } from "@tanstack/react-router"
import { useOrganizations } from "@/features/organization/hooks"
import { OrganizationIPAllowlistForm } from "@/features/organization/components/organization-ip-allowlist-form"
import type { OrganizationRole } from "@/types/organization"

export function OrganizationSettingsSecurityPage() {
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }

  const { data: listData } = useOrganizations()
  const role = listData?.data?.find((w) => w.id === organizationId)?.role as
    | OrganizationRole
    | undefined
  const isOwner = role === "owner"

  return (
    <OrganizationIPAllowlistForm
      organizationId={organizationId}
      isOwner={isOwner}
    />
  )
}
