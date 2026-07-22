import { useParams } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { PageHeader } from "@/components/shared/page-header"
import { AuditLogTable } from "@/features/organization/components/audit-log-table"

export function AuditLogPage() {
  const { t } = useTranslation()
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t("organization.tabs.auditLog")}
        description={t("organization.auditLog.pageDescription")}
      />
      <AuditLogTable organizationId={organizationId} />
    </div>
  )
}
