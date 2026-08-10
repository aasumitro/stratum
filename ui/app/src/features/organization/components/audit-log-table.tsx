import { useEffect, useState } from "react"
import { useSearch } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import type { ReactNode } from "react"
import {
  IconInfoCircle,
  IconX,
  IconUser,
  IconRoute,
  IconMapPin,
  IconDeviceDesktop,
  IconClock,
  IconCode,
} from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { DataTable, type DataTableColumn } from "@/components/shared/data-table"
import { DataTablePagination } from "@/components/shared/pagination"
import { StatusBadge } from "@/components/shared/status-badge"
import { useCursorAccumulator } from "@/lib/api/use-cursor-accumulator"
import {
  useAuditLog,
  type AuditLogFilter,
} from "@/features/organization/hooks/use-audit-log"
import { useOrganizationMembers } from "@/features/organization/hooks/use-members"
import { humanizeAuditAction } from "@/features/organization/utils/humanize-audit-action"
import { initials, describeDevice } from "@/lib/format"
import { cn } from "@/lib/ui"
import type { AuditEvent } from "@/types/organization"
import type { SettingsSearch } from "@/routes/_protected/organization/$organizationId/settings"

function rangeToFrom(range: SettingsSearch["range"]): string | undefined {
  if (range === "all") return undefined
  const days = { "7d": 7, "30d": 30, "90d": 90 }[range ?? "7d"]
  const d = new Date()
  d.setDate(d.getDate() - days)
  d.setHours(0, 0, 0, 0)
  return d.toISOString()
}

// `metadata` comes back from the API as a base64-encoded JSON string (e.g.
// "e30=" for "{}"), not a plain object — decode it before display, rather
// than rendering the base64 itself as if it were the payload.
function decodeMetadata(metadata: unknown): unknown {
  if (typeof metadata !== "string") return metadata
  try {
    return JSON.parse(atob(metadata))
  } catch {
    return metadata
  }
}

function isEmptyPayload(value: unknown): boolean {
  return (
    value == null ||
    (typeof value === "object" && Object.keys(value).length === 0)
  )
}

function DetailField({
  icon: Icon,
  label,
  children,
}: {
  icon: typeof IconUser
  label: string
  children: ReactNode
}) {
  return (
    <div className="flex gap-3">
      <Icon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0 flex-1">
        <p className="text-xs font-medium text-muted-foreground">{label}</p>
        <div className="mt-0.5 text-sm">{children}</div>
      </div>
    </div>
  )
}

interface Props {
  organizationId: string
  onTotalChange: (total: number) => void
}

/**
 * Audit Log's results table + row-detail pane. Filters live in the sibling
 * `AuditLogFilters` (rendered inline in the overlay's header) — both read
 * the same Settings-route URL search params directly, no prop coupling
 * between the two. The detail pane is a right-side slide-in confined to
 * `OverlayPanel`'s own bounds (its `relative overflow-hidden` popup), not a
 * viewport-edge Sheet — `absolute` positions against that ancestor.
 */
export function AuditLogTable({ organizationId, onTotalChange }: Props) {
  const { t } = useTranslation()
  const search = useSearch({
    from: "/_protected/organization/$organizationId/settings",
  })
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [detail, setDetail] = useState<AuditEvent | null>(null)

  const { data: membersData } = useOrganizationMembers(organizationId)
  const members = membersData?.data ?? []

  function resolveActor(event: AuditEvent) {
    const isSystem = event.actor === "system" || event.actor === "anonymous"
    if (isSystem) {
      return {
        label: t("organization.auditLog.systemActor"),
        isSystem,
        email: undefined,
      }
    }
    const member = members.find((m) => m.auth_sub === event.actor)
    return {
      label: member?.full_name ?? member?.email ?? event.actor.slice(0, 12),
      isSystem,
      email: member?.email,
    }
  }

  const filter: AuditLogFilter = {
    actor: search.actor,
    action: search.action,
    resource: search.resource,
    from: rangeToFrom(search.range),
  }

  // Reset cursor to page 1 whenever any filter changes
  const filterKey = JSON.stringify(filter)
  const [prevFilterKey, setPrevFilterKey] = useState(filterKey)
  if (filterKey !== prevFilterKey) {
    setPrevFilterKey(filterKey)
    setCursor(undefined)
  }

  const { data, isLoading, isError, isFetching, refetch } = useAuditLog(
    organizationId,
    filter,
    cursor
  )
  const { items, nextCursor } = useCursorAccumulator(data, cursor)
  const hasFilters = !!(search.actor || search.action || search.resource)

  useEffect(() => {
    onTotalChange(items.length)
  }, [items.length, onTotalChange])

  function copyEventJSON(event: AuditEvent) {
    void navigator.clipboard.writeText(JSON.stringify(event, null, 2))
    toast.success(t("organization.auditLog.copiedJson"))
  }

  const columns: DataTableColumn<AuditEvent>[] = [
    {
      key: "actor",
      header: t("organization.auditLog.actorCol"),
      cell: (event) => {
        const { label, isSystem } = resolveActor(event)
        return (
          <span className="flex items-center gap-2">
            <Avatar className="size-6 shrink-0 rounded-lg">
              <AvatarFallback className="rounded-lg text-[10px]">
                {isSystem ? "⚙" : initials(label)}
              </AvatarFallback>
            </Avatar>
            <span className="truncate text-sm">{label}</span>
          </span>
        )
      },
    },
    {
      key: "action",
      header: t("organization.auditLog.methodCol"),
      cell: (event) => (
        <StatusBadge
          status={event.status_code < 400 ? "active" : "failed"}
          label={humanizeAuditAction(event.action, event.resource)}
        />
      ),
    },
    {
      key: "resource",
      header: t("organization.auditLog.resourceCol"),
      className: "max-w-48 truncate font-mono text-xs text-muted-foreground",
      cell: (event) => event.resource,
    },
    {
      key: "ip",
      header: t("organization.auditLog.ipCol"),
      className: "font-mono text-xs text-muted-foreground",
      cell: (event) => event.ip || "—",
    },
    {
      key: "when",
      header: t("organization.auditLog.whenCol"),
      className: "text-xs whitespace-nowrap text-muted-foreground",
      cell: (event) => new Date(event.created_at).toLocaleString(),
    },
  ]

  return (
    <div className="flex flex-col">
      <DataTable
        className="rounded-none border-0"
        columns={columns}
        rows={items}
        rowKey={(event) => event.id}
        isLoading={isLoading}
        isError={isError}
        onRetry={() => void refetch()}
        onRowClick={setDetail}
        empty={{
          icon: IconInfoCircle,
          title: t("organization.auditLog.noEvents"),
          description: hasFilters
            ? t("organization.auditLog.clearFiltersHint")
            : undefined,
        }}
      />

      <div className="p-0 pt-4 md:p-6 md:pt-4">
        <DataTablePagination
          mode="cursor"
          hasMore={!!nextCursor}
          isLoadingMore={isFetching}
          onLoadMore={() => setCursor(nextCursor)}
          loadMoreLabel={t("common.loadMore")}
        />
      </div>

      {detail && (
        <button
          type="button"
          aria-label={t("common.close")}
          className="absolute inset-0 z-10 bg-black/20"
          onClick={() => setDetail(null)}
        />
      )}
      <div
        className={cn(
          "absolute inset-y-0 right-0 z-20 flex w-full flex-col border-l bg-popover shadow-xl transition-transform duration-200 sm:w-[32rem]",
          detail ? "translate-x-0" : "translate-x-full"
        )}
      >
        {detail && (
          <>
            <div className="flex shrink-0 items-start justify-between gap-3 border-b px-4 py-3">
              <div className="min-w-0">
                <p className="font-heading text-sm font-medium">
                  {t("organization.auditLog.eventDetail")}
                </p>
                <div className="mt-1.5">
                  <StatusBadge
                    status={detail.status_code < 400 ? "active" : "failed"}
                    label={humanizeAuditAction(detail.action, detail.resource)}
                  />
                </div>
              </div>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={t("common.close")}
                onClick={() => setDetail(null)}
              >
                <IconX className="size-4" />
              </Button>
            </div>
            <div className="flex-1 overflow-y-auto px-4 py-4">
              <div className="flex flex-col gap-4">
                {(() => {
                  const actor = resolveActor(detail)
                  return (
                    <DetailField
                      icon={IconUser}
                      label={t("organization.auditLog.actorCol")}
                    >
                      <div className="flex items-center gap-2">
                        <Avatar className="size-6 shrink-0 rounded-lg">
                          <AvatarFallback className="rounded-lg text-[10px]">
                            {actor.isSystem ? "⚙" : initials(actor.label)}
                          </AvatarFallback>
                        </Avatar>
                        <div className="min-w-0">
                          <p className="truncate font-medium">{actor.label}</p>
                          {actor.email && actor.email !== actor.label && (
                            <p className="truncate text-xs text-muted-foreground">
                              {actor.email}
                            </p>
                          )}
                        </div>
                      </div>
                    </DetailField>
                  )
                })()}

                <DetailField
                  icon={IconClock}
                  label={t("organization.auditLog.whenCol")}
                >
                  {new Date(detail.created_at).toLocaleString()}
                </DetailField>

                <DetailField
                  icon={IconRoute}
                  label={t("organization.auditLog.resourceCol")}
                >
                  <div className="flex flex-col gap-1">
                    <StatusBadge
                      status={detail.status_code < 400 ? "active" : "failed"}
                      label={detail.action}
                    />
                    <p className="font-mono text-xs break-all text-muted-foreground">
                      {detail.resource}
                    </p>
                  </div>
                </DetailField>

                <DetailField
                  icon={IconMapPin}
                  label={t("organization.auditLog.ipCol")}
                >
                  <span className="font-mono text-xs">{detail.ip || "—"}</span>
                </DetailField>

                <DetailField
                  icon={IconDeviceDesktop}
                  label={t("organization.auditLog.deviceLabel")}
                >
                  <span title={detail.user_agent || undefined}>
                    {detail.user_agent
                      ? describeDevice(detail.user_agent).label
                      : "—"}
                  </span>
                </DetailField>

                <DetailField
                  icon={IconCode}
                  label={t("organization.auditLog.payload")}
                >
                  {(() => {
                    const payload = decodeMetadata(detail.metadata)
                    return isEmptyPayload(payload) ? (
                      <p className="text-xs text-muted-foreground">
                        {t("organization.auditLog.noPayload")}
                      </p>
                    ) : (
                      <pre className="overflow-x-auto rounded-lg bg-muted p-2 font-mono text-[11px]">
                        {JSON.stringify(payload, null, 2)}
                      </pre>
                    )
                  })()}
                </DetailField>
              </div>
            </div>
            <div className="flex shrink-0 justify-end border-t px-4 py-3">
              <Button
                variant="outline"
                size="sm"
                onClick={() => copyEventJSON(detail)}
              >
                {t("organization.auditLog.copyJson")}
              </Button>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
