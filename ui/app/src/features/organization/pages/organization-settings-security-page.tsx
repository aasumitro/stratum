import { useParams } from "@tanstack/react-router"
import { usePermissions } from "@/hooks/use-permissions"
import { OrganizationIPAllowlistForm } from "@/features/organization/components/organization-ip-allowlist-form"

export function OrganizationSettingsSecurityPage() {
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }

  const { isOwner } = usePermissions()

  return (
    <OrganizationIPAllowlistForm
      organizationId={organizationId}
      isOwner={isOwner}
    />
  )
}
