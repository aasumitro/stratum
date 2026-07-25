import { useParams } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { useOrganization } from "@/features/organization/hooks"
import { usePermissions } from "@/hooks/use-permissions"
import { OrganizationDangerZone } from "@/features/organization/components/organization-danger-zone"
import { AccessDeniedExplainer } from "@/components/shared/permission-guard"

export function OrganizationSettingsDangerPage() {
  const { t } = useTranslation()
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }

  const { data: wsData } = useOrganization(organizationId)
  const { isOwner } = usePermissions()
  const organization = wsData?.data

  if (isOwner && organization) {
    return (
      <OrganizationDangerZone
        organizationId={organizationId}
        slug={organization.slug}
        status={organization.status}
      />
    )
  }

  return (
    <AccessDeniedExplainer
      title={t("organization.settings.dangerZoneDeniedTitle")}
      description={t("organization.settings.dangerZoneDeniedDescription")}
    />
  )
}
