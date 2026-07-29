import { useTranslation } from "react-i18next"
import { IconInfoCircle } from "@tabler/icons-react"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Skeleton } from "@/components/ui/skeleton"
import { EmptyState } from "@/components/shared/empty-state"
import { StatusBadge } from "@/components/shared/status-badge"
import { useAuditLog } from "@/features/organization/hooks/use-audit-log"
import { useOrganizationMembers } from "@/features/organization/hooks/use-members"
import { humanizeAuditAction } from "@/features/organization/utils/humanize-audit-action"
import { initials, timeAgo } from "@/lib/format"

interface AuditLogSectionPreviewProps {
  organizationId: string
}

/**
 * Audit Log section content for Settings: last 5 events, no filters —
 * a static recent-activity read. "View Full Log" (rendered by the page)
 * opens the full filterable/exportable table in an overlay.
 */
export function AuditLogSectionPreview({
  organizationId,
}: AuditLogSectionPreviewProps) {
  const { t } = useTranslation()
  const { data, isLoading } = useAuditLog(organizationId, {}, undefined, 5)
  const { data: membersData } = useOrganizationMembers(organizationId)
  const members = membersData?.data ?? []
  const events = data?.data ?? []

  if (isLoading) {
    return (
      <div className="flex flex-col gap-3">
        {[1, 2].map((i) => (
          <Skeleton key={i} className="h-12 w-full" />
        ))}
      </div>
    )
  }

  if (!events.length) {
    return (
      <div className="rounded-xl border bg-card">
        <EmptyState
          icon={IconInfoCircle}
          title={t("organization.auditLog.noEvents")}
        />
      </div>
    )
  }

  return (
    <div className="divide-y rounded-xl border">
      {events.map((event) => {
        const member = members.find((m) => m.auth_sub === event.actor)
        const isSystem = event.actor === "system" || event.actor === "anonymous"
        const label = isSystem
          ? t("organization.auditLog.systemActor")
          : (member?.full_name ?? member?.email ?? event.actor.slice(0, 12))

        return (
          <div key={event.id} className="flex items-center gap-3 px-3 py-2.5">
            <Avatar className="size-6 shrink-0 rounded-lg">
              <AvatarFallback className="rounded-lg text-[10px]">
                {isSystem ? "⚙" : initials(label)}
              </AvatarFallback>
            </Avatar>
            <span className="truncate text-sm">{label}</span>
            <StatusBadge
              status={event.status_code < 400 ? "active" : "failed"}
              label={humanizeAuditAction(event.action, event.resource)}
            />
            <span className="hidden max-w-48 truncate font-mono text-xs text-muted-foreground sm:block md:max-w-64 lg:max-w-xs">
              {event.resource}
            </span>
            <div className="flex-1" />

            <div className="flex shrink-0 items-center gap-4">
              {event.ip && (
                <span className="hidden font-mono text-xs text-muted-foreground sm:block">
                  {event.ip}
                </span>
              )}
              <span className="text-xs whitespace-nowrap text-muted-foreground">
                {timeAgo(event.created_at)}
              </span>
            </div>
          </div>
        )
      })}
    </div>
  )
}
