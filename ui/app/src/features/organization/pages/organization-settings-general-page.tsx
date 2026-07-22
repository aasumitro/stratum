import { useParams } from "@tanstack/react-router"
import {
  useOrganizations,
  useOrganization,
} from "@/features/organization/hooks"
import { OrganizationBasicForm } from "@/features/organization/components/organization-basic-form"
import { OrganizationLogoForm } from "@/features/organization/components/organization-logo-form"
import { OrganizationLocaleForm } from "@/features/organization/components/organization-locale-form"
import type { OrganizationRole } from "@/types/organization"

export function OrganizationSettingsGeneralPage() {
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }

  const { data: listData } = useOrganizations()
  const { data: wsData } = useOrganization(organizationId)

  const role = listData?.data?.find((w) => w.id === organizationId)?.role as
    | OrganizationRole
    | undefined
  const isOwner = role === "owner"
  const isAdminUp = role === "owner" || role === "admin"
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
