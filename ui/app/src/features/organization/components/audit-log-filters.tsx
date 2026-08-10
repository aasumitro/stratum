import { useState } from "react"
import { useNavigate, useSearch } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { IconDownload, IconInfoCircle, IconX } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { downloadFile } from "@/lib/api/download"
import { useOrganizationMembers } from "@/features/organization/hooks/use-members"
import {
  auditLogExportUrl,
  type AuditLogFilter,
} from "@/features/organization/hooks/use-audit-log"
import type { SettingsSearch } from "@/routes/_protected/organization/$organizationId/settings"

const ACTIONS = ["POST", "PATCH", "PUT", "DELETE"] as const
const RANGES = ["7d", "30d", "90d", "all"] as const

function rangeToFrom(range: SettingsSearch["range"]): string | undefined {
  if (range === "all") return undefined
  const days = { "7d": 7, "30d": 30, "90d": 90 }[range ?? "7d"]
  const d = new Date()
  d.setDate(d.getDate() - days)
  return d.toISOString()
}

interface Props {
  organizationId: string
  total: number
}

/**
 * Audit Log's filter/export controls — rendered inline in the overlay's
 * header row, next to the title, instead of a separate row in the body.
 * Shares filter state with `AuditLogTable` via the Settings route's URL
 * search params; the row count itself is passed down from the parent page,
 * which owns both this component and `AuditLogTable`.
 */
export function AuditLogFilters({ organizationId, total }: Props) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const search = useSearch({
    from: "/_protected/organization/$organizationId/settings",
  })
  const { data: membersData } = useOrganizationMembers(organizationId)
  const members = membersData?.data ?? []

  function setSearch(next: Partial<SettingsSearch>) {
    void navigate({
      to: "/organization/$organizationId/settings",
      params: { organizationId },
      search: { ...search, ...next },
      resetScroll: false,
    })
  }

  function getActorLabel(val: string | undefined) {
    if (!val || val === "all") return t("organization.auditLog.allActors")
    if (val === "system" || val === "anonymous")
      return t("organization.auditLog.systemActor")
    const m = members.find((m) => m.auth_sub === val)
    return m ? (m.full_name ?? m.email ?? m.auth_sub) : val
  }

  const filter: AuditLogFilter = {
    actor: search.actor,
    action: search.action,
    resource: search.resource,
    from: rangeToFrom(search.range),
  }

  const [resourceText, setResourceText] = useState(search.resource ?? "")
  const [prevResource, setPrevResource] = useState(search.resource)

  if (search.resource !== prevResource) {
    setPrevResource(search.resource)
    setResourceText(search.resource ?? "")
  }

  async function exportCSV() {
    await downloadFile(
      auditLogExportUrl(organizationId, filter),
      "audit-log.csv",
      "blob"
    )
  }

  const hasFilters = !!(
    search.actor ||
    search.action ||
    search.resource ||
    (search.range && search.range !== "7d")
  )

  return (
    <div className="grid w-full grid-cols-2 gap-2 sm:flex sm:w-auto sm:flex-1 sm:flex-wrap sm:items-center">
      <Select
        value={search.actor ?? "all"}
        onValueChange={(v) =>
          setSearch({ actor: !v || v === "all" ? undefined : v })
        }
      >
        <SelectTrigger className="w-full sm:w-32">
          <SelectValue>{getActorLabel(search.actor)}</SelectValue>
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
        <SelectTrigger className="w-full sm:w-28">
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
        value={resourceText}
        onChange={(e) => setResourceText(e.target.value)}
        onBlur={() => setSearch({ resource: resourceText.trim() || undefined })}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            setSearch({ resource: resourceText.trim() || undefined })
          }
        }}
        className="w-full sm:w-32"
      />

      <Select
        value={search.range ?? "7d"}
        onValueChange={(v) =>
          setSearch({ range: v as SettingsSearch["range"] })
        }
      >
        <SelectTrigger className="w-full sm:w-32">
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

      {hasFilters && (
        <Button
          variant="link"
          size="sm"
          className="px-2 text-[10px] font-bold text-muted-foreground uppercase hover:text-foreground"
          onClick={() =>
            setSearch({
              actor: undefined,
              action: undefined,
              resource: undefined,
              range: undefined,
            })
          }
        >
          <IconX className="mr-1 size-3" />
          {t("common.clear")}
        </Button>
      )}

      <div className="col-span-2 flex items-center justify-between gap-2 sm:col-span-1 sm:ml-auto sm:justify-start">
        <span className="text-xs text-muted-foreground">
          {t("common.showingRecords", { count: total })}
        </span>
        <Tooltip>
          <TooltipTrigger
            render={<IconInfoCircle className="size-4 text-muted-foreground" />}
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
    </div>
  )
}
