import { useParams } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import {
  useOrganizations,
  useOrganization,
} from "@/features/organization/hooks"
import { OrganizationDangerZone } from "@/features/organization/components/organization-danger-zone"
import { AccessDeniedExplainer } from "@/components/shared/permission-guard"
import type { OrganizationRole } from "@/types/organization"

export function OrganizationSettingsDangerPage() {
  const { t } = useTranslation()
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }

  const { data: listData } = useOrganizations()
  const { data: wsData } = useOrganization(organizationId)

  const role = listData?.data?.find((w) => w.id === organizationId)?.role as
    | OrganizationRole
    | undefined
  const isOwner = role === "owner"
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
