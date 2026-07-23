import { useParams } from "@tanstack/react-router"
import { useOrganization } from "@/features/organization/hooks"
import { usePermissions } from "@/hooks/use-permissions"
import { OrganizationBasicForm } from "@/features/organization/components/organization-basic-form"
import { OrganizationLogoForm } from "@/features/organization/components/organization-logo-form"
import { OrganizationLocaleForm } from "@/features/organization/components/organization-locale-form"

export function OrganizationSettingsGeneralPage() {
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }

  const { data: wsData } = useOrganization(organizationId)
  const { isOwner, isAdminUp } = usePermissions()
  const organization = wsData?.data

  return (
    <div className="flex flex-col gap-6">
      {isAdminUp && organization && (
        <OrganizationLogoForm organization={organization} />
      )}
      <OrganizationBasicForm
        organizationId={organizationId}
        isOwner={isOwner}
      />
      <OrganizationLocaleForm
        organizationId={organizationId}
        isOwner={isOwner}
      />
    </div>
  )
}
