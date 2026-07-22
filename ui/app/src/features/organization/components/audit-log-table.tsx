import { useState } from "react"
import { useNavigate, useSearch } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconDownload, IconInfoCircle } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { DataTable, type DataTableColumn } from "@/components/shared/data-table"
import { DataTablePagination } from "@/components/shared/pagination"
import { FilterBar, type FilterChip } from "@/components/shared/filter-bar"
import { SideDrawer } from "@/components/shared/side-drawer"
import { StatusBadge } from "@/components/shared/status-badge"
import { downloadFile } from "@/lib/api/download"
import { useCursorAccumulator } from "@/lib/api/use-cursor-accumulator"
import {
  useAuditLog,
  auditLogExportUrl,
  useOrganizationMembers,
  type AuditLogFilter,
} from "@/features/organization/hooks"
import { humanizeAuditAction } from "@/features/organization/utils/humanize-audit-action"
import { initials } from "@/lib/format"
import type { AuditEvent } from "@/types/organization"
import type { AuditLogSearch } from "@/routes/_protected/organization/$organizationId/audit-log"

const ACTIONS = ["POST", "PATCH", "PUT", "DELETE"] as const
const RANGES = ["7d", "30d", "90d", "all"] as const

function rangeToFrom(range: AuditLogSearch["range"]): string | undefined {
  if (!range || range === "all") return undefined
  const days = { "7d": 7, "30d": 30, "90d": 90 }[range]
  const d = new Date()
  d.setDate(d.getDate() - days)
  return d.toISOString()
}

interface Props {
  organizationId: string
}

export function AuditLogTable({ organizationId }: Props) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const search = useSearch({
    from: "/_protected/organization/$organizationId/audit-log",
  })
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [detail, setDetail] = useState<AuditEvent | null>(null)

  const { data: membersData } = useOrganizationMembers(organizationId)
  const members = membersData?.data ?? []

  const filter: AuditLogFilter = {
    actor: search.actor,
    action: search.action,
    resource: search.resource,
    from: rangeToFrom(search.range),
  }

  const { data, isLoading, isError, isFetching, refetch } = useAuditLog(
    organizationId,
    filter,
    cursor
  )
  const { items, nextCursor } = useCursorAccumulator(data, cursor)

  function setSearch(next: Partial<AuditLogSearch>) {
    setCursor(undefined)
    void navigate({
      to: "/organization/$organizationId/audit-log",
      params: { organizationId },
      search: { ...search, ...next },
    })
  }

  const chips: FilterChip[] = []
  if (search.actor) {
    const m = members.find((mm) => mm.auth_sub === search.actor)
    chips.push({
      key: "actor",
      label: `${t("organization.auditLog.actorCol")}: ${m?.full_name ?? m?.email ?? search.actor}`,
      onRemove: () => setSearch({ actor: undefined }),
    })
  }
  if (search.action) {
    chips.push({
      key: "action",
      label: `${t("organization.auditLog.methodCol")}: ${search.action}`,
      onRemove: () => setSearch({ action: undefined }),
    })
  }
  if (search.resource) {
    chips.push({
      key: "resource",
      label: `${t("organization.auditLog.resourceCol")}: ${search.resource}`,
      onRemove: () => setSearch({ resource: undefined }),
    })
  }

  async function exportCSV() {
    await downloadFile(
      auditLogExportUrl(organizationId, filter),
      "audit-log.csv",
      "blob"
    )
  }

  function copyEventJSON(event: AuditEvent) {
    void navigator.clipboard.writeText(JSON.stringify(event, null, 2))
    toast.success(t("organization.auditLog.copiedJson"))
  }

  const columns: DataTableColumn<AuditEvent>[] = [
    {
      key: "actor",
      header: t("organization.auditLog.actorCol"),
      cell: (event) => {
        const member = members.find((m) => m.auth_sub === event.actor)
        const isSystem = event.actor === "system" || event.actor === "anonymous"
        const label = isSystem
          ? t("organization.auditLog.systemActor")
          : (member?.full_name ?? member?.email ?? event.actor.slice(0, 12))
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
    <div className="flex flex-col gap-4">
      <FilterBar
        chips={chips}
        onClearAll={() =>
          setSearch({
            actor: undefined,
            action: undefined,
            resource: undefined,
          })
        }
      >
        <Select
          value={search.actor ?? "all"}
          onValueChange={(v) =>
            setSearch({ actor: !v || v === "all" ? undefined : v })
          }
        >
          <SelectTrigger className="h-8 w-36 text-xs">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">
              {t("organization.auditLog.allActors")}
            </SelectItem>
            {members.map((m) => (
              <SelectItem key={m.auth_sub} value={m.auth_sub}>
                {m.full_name ?? m.email ?? m.auth_sub}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <Select
          value={search.action ?? "all"}
          onValueChange={(v) =>
            setSearch({ action: !v || v === "all" ? undefined : v })
          }
        >
          <SelectTrigger className="h-8 w-32 text-xs">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">
              {t("organization.auditLog.allActions")}
            </SelectItem>
            {ACTIONS.map((a) => (
              <SelectItem key={a} value={a}>
                {a}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <Input
          placeholder={t("organization.auditLog.resourceCol")}
          defaultValue={search.resource ?? ""}
          onBlur={(e) =>
            setSearch({ resource: e.target.value.trim() || undefined })
          }
          className="h-8 w-40 text-xs"
        />

        <Select
          value={search.range ?? "7d"}
          onValueChange={(v) =>
            setSearch({ range: v as AuditLogSearch["range"] })
          }
        >
          <SelectTrigger className="h-8 w-36 text-xs">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {RANGES.map((r) => (
              <SelectItem key={r} value={r}>
                {t(`organization.auditLog.range.${r}`)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <div className="ml-auto flex items-center gap-2">
          <Tooltip>
            <TooltipTrigger
              render={
                <IconInfoCircle className="size-4 text-muted-foreground" />
              }
            />
            <TooltipContent>
              {t("organization.auditLog.retentionNote")}
            </TooltipContent>
          </Tooltip>
          <Button variant="outline" size="sm" onClick={() => void exportCSV()}>
            <IconDownload data-icon="inline-start" />
            {t("organization.auditLog.exportCsv")}
          </Button>
        </div>
      </FilterBar>

      <DataTable
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
          description:
            chips.length > 0
              ? t("organization.auditLog.clearFiltersHint")
              : undefined,
        }}
      />

      <DataTablePagination
        mode="cursor"
        hasMore={!!nextCursor}
        isLoadingMore={isFetching}
        onLoadMore={() => setCursor(nextCursor)}
        loadMoreLabel={t("common.loadMore")}
      />

      <SideDrawer
        open={!!detail}
        onOpenChange={(open) => !open && setDetail(null)}
        title={t("organization.auditLog.eventDetail")}
        description={
          detail
            ? humanizeAuditAction(detail.action, detail.resource)
            : undefined
        }
        footer={
          detail && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => copyEventJSON(detail)}
            >
              {t("organization.auditLog.copyJson")}
            </Button>
          )
        }
      >
        {detail && (
          <div className="flex flex-col gap-3 py-2 text-sm">
            <div>
              <p className="text-xs font-semibold text-muted-foreground">
                {t("organization.auditLog.actorCol")}
              </p>
              <p className="font-mono text-xs">{detail.actor}</p>
            </div>
            <div>
              <p className="text-xs font-semibold text-muted-foreground">
                {t("organization.auditLog.resourceCol")}
              </p>
              <p className="font-mono text-xs">
                {detail.action} {detail.resource}
              </p>
            </div>
            <div>
              <p className="text-xs font-semibold text-muted-foreground">
                {t("organization.auditLog.ipCol")}
              </p>
              <p className="font-mono text-xs">
                {detail.ip || "—"} · {detail.user_agent || "—"}
              </p>
            </div>
            {detail.metadata != null && (
              <div>
                <p className="text-xs font-semibold text-muted-foreground">
                  {t("organization.auditLog.payload")}
                </p>
                <pre className="mt-1 overflow-x-auto rounded-lg bg-muted p-2 font-mono text-[11px]">
                  {JSON.stringify(detail.metadata, null, 2)}
                </pre>
              </div>
            )}
          </div>
        )}
      </SideDrawer>
    </div>
  )
}
