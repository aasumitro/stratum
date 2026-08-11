import { useState } from "react"
import { useParams } from "@tanstack/react-router"
import {
  IconChevronLeft,
  IconChevronRight,
  IconClipboardList,
  IconDownload,
  IconSearch,
} from "@tabler/icons-react"
import type {
  AuditEvent,
  AuditFilter,
} from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import {
  AUDIT_PAGE_SIZE,
  exportAuditCSV,
  useAuditCount,
  useAuditEvents,
} from "@/hooks/use-audit"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { cn } from "@/lib/ui"
import { AuditDetailSheet } from "./audit-detail-sheet"
import { methodColor, statusColor } from "./audit-colors"

const EMPTY_FILTER: Omit<AuditFilter, "limit" | "offset"> = {
  actor: "",
  action: "",
  organization_id: "",
  status_code: 0,
  from: "",
  to: "",
}

export function AuditPage() {
  const { projectId } = useParams({ from: "/studio/$projectId/audit" })

  const [filter, setFilter] = useState(EMPTY_FILTER)
  const [pendingFilter, setPendingFilter] = useState(EMPTY_FILTER)
  const [offset, setOffset] = useState(0)
  const [exporting, setExporting] = useState(false)
  const [detail, setDetail] = useState<AuditEvent | null>(null)

  const activeFilter: AuditFilter = {
    ...filter,
    limit: AUDIT_PAGE_SIZE,
    offset,
  }
  const {
    data: events,
    isLoading,
    error,
    refetch,
  } = useAuditEvents(projectId, activeFilter)
  const { data: total } = useAuditCount(projectId, filter)

  const hasMore = (events?.length ?? 0) === AUDIT_PAGE_SIZE
  const hasPrev = offset > 0
  const currentPage = Math.floor(offset / AUDIT_PAGE_SIZE) + 1

  function applyFilter() {
    setFilter(pendingFilter)
    setOffset(0)
  }

  function clearFilter() {
    setPendingFilter(EMPTY_FILTER)
    setFilter(EMPTY_FILTER)
    setOffset(0)
  }

  async function handleExport() {
    setExporting(true)
    await exportAuditCSV(projectId, { ...filter, limit: 5000, offset: 0 })
    setExporting(false)
  }

  const isFiltered = Object.values(filter).some((v) => v !== "" && v !== 0)

  return (
    <div className="flex min-h-full flex-col">
      <header className="flex items-center justify-between border-b px-6 py-4">
        <div className="flex items-center gap-3">
          <IconClipboardList className="size-4 text-muted-foreground" />
          <div className="flex flex-col gap-0.5">
            <h1 className="text-sm font-semibold">Audit Log</h1>
            <p className="text-xs text-muted-foreground">
              Cross-organization API activity
              {total !== undefined && (
                <span className="ml-1 font-medium text-foreground">
                  ({total.toLocaleString()} events)
                </span>
              )}
            </p>
          </div>
        </div>
        <Button
          variant="outline"
          size="sm"
          className="gap-2"
          onClick={handleExport}
          disabled={exporting}
        >
          <IconDownload className="size-4" />
          {exporting ? "Exporting…" : "Export CSV"}
        </Button>
      </header>

      <div className="space-y-5 p-6">
        {/* Filter bar */}
        <div className="space-y-3 rounded-lg border bg-card/40 p-4">
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            {/* Actor */}
            <div className="space-y-1">
              <Label className="text-xs text-muted-foreground">Actor</Label>
              <div className="relative">
                <IconSearch className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
                <Input
                  className="h-8 pl-7 text-sm"
                  placeholder="auth_sub or email"
                  value={pendingFilter.actor}
                  onChange={(e) =>
                    setPendingFilter((p) => ({ ...p, actor: e.target.value }))
                  }
                />
              </div>
            </div>

            {/* Method */}
            <div className="space-y-1">
              <Label className="text-xs text-muted-foreground">Method</Label>
              <select
                value={pendingFilter.action}
                onChange={(e) =>
                  setPendingFilter((p) => ({ ...p, action: e.target.value }))
                }
                className={cn(
                  "h-8 w-full rounded-md border border-input bg-transparent px-3 text-sm shadow-sm",
                  "transition-colors focus:ring-1 focus:ring-ring focus:outline-hidden"
                )}
              >
                <option value="">All</option>
                <option value="GET">GET</option>
                <option value="POST">POST</option>
                <option value="PATCH">PATCH</option>
                <option value="PUT">PUT</option>
                <option value="DELETE">DELETE</option>
              </select>
            </div>

            {/* Organization */}
            <div className="space-y-1">
              <Label className="text-xs text-muted-foreground">
                Organization ID
              </Label>
              <Input
                className="h-8 font-mono text-sm"
                placeholder="UUID"
                value={pendingFilter.organization_id}
                onChange={(e) =>
                  setPendingFilter((p) => ({
                    ...p,
                    organization_id: e.target.value.trim(),
                  }))
                }
              />
            </div>

            {/* Status code */}
            <div className="space-y-1">
              <Label className="text-xs text-muted-foreground">
                Status Code
              </Label>
              <Input
                className="h-8 text-sm"
                placeholder="e.g. 400"
                type="number"
                min={0}
                value={
                  pendingFilter.status_code === 0
                    ? ""
                    : pendingFilter.status_code
                }
                onChange={(e) =>
                  setPendingFilter((p) => ({
                    ...p,
                    status_code: Number(e.target.value) || 0,
                  }))
                }
              />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            {/* From */}
            <div className="space-y-1">
              <Label className="text-xs text-muted-foreground">From</Label>
              <Input
                className="h-8 text-sm"
                type="datetime-local"
                value={
                  pendingFilter.from ? pendingFilter.from.slice(0, 16) : ""
                }
                onChange={(e) =>
                  setPendingFilter((p) => ({
                    ...p,
                    from: e.target.value
                      ? new Date(e.target.value).toISOString()
                      : "",
                  }))
                }
              />
            </div>

            {/* To */}
            <div className="space-y-1">
              <Label className="text-xs text-muted-foreground">To</Label>
              <Input
                className="h-8 text-sm"
                type="datetime-local"
                value={pendingFilter.to ? pendingFilter.to.slice(0, 16) : ""}
                onChange={(e) =>
                  setPendingFilter((p) => ({
                    ...p,
                    to: e.target.value
                      ? new Date(e.target.value).toISOString()
                      : "",
                  }))
                }
              />
            </div>

            <div className="col-span-2 flex items-end gap-2">
              <Button size="sm" onClick={applyFilter} className="gap-1.5">
                Apply filters
              </Button>
              {isFiltered && (
                <Button size="sm" variant="ghost" onClick={clearFilter}>
                  Clear
                </Button>
              )}
            </div>
          </div>
        </div>

        {/* Table */}
        <div className="rounded-md border">
          {isLoading ? (
            <div className="divide-y">
              {Array.from({ length: 8 }).map((_, i) => (
                <div key={i} className="px-4 py-3">
                  <Skeleton className="h-4 w-full" />
                </div>
              ))}
            </div>
          ) : error ? (
            <div className="flex flex-col items-center gap-2 py-12 text-sm text-muted-foreground">
              <span>Failed to load audit log</span>
              <Button variant="outline" size="sm" onClick={() => refetch()}>
                Try again
              </Button>
            </div>
          ) : !events || events.length === 0 ? (
            <div className="flex flex-col items-center gap-2 py-12 text-muted-foreground">
              <IconClipboardList className="size-8 opacity-30" />
              <span className="text-sm">No events found</span>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Time</TableHead>
                  <TableHead>Actor</TableHead>
                  <TableHead>Method</TableHead>
                  <TableHead>Resource</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Organization</TableHead>
                  <TableHead>IP</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {events.map((ev) => (
                  <TableRow
                    key={ev.id}
                    className="cursor-pointer hover:bg-muted/50"
                    onClick={() => setDetail(ev)}
                  >
                    <TableCell className="text-xs whitespace-nowrap text-muted-foreground">
                      {new Date(ev.created_at).toLocaleString()}
                    </TableCell>
                    <TableCell
                      className="max-w-35 truncate font-mono text-xs"
                      title={ev.actor}
                    >
                      {ev.actor}
                    </TableCell>
                    <TableCell>
                      <span
                        className={cn(
                          "inline-flex rounded px-1.5 py-0.5 font-mono text-xs font-medium",
                          methodColor(ev.action)
                        )}
                      >
                        {ev.action}
                      </span>
                    </TableCell>
                    <TableCell
                      className="max-w-60 truncate font-mono text-xs"
                      title={ev.resource}
                    >
                      {ev.resource}
                    </TableCell>
                    <TableCell>
                      <span
                        className={cn(
                          "inline-flex items-center rounded border px-1.5 py-0.5 text-xs font-medium",
                          statusColor(ev.status_code)
                        )}
                      >
                        {ev.status_code}
                      </span>
                    </TableCell>
                    <TableCell
                      className="max-w-30 truncate font-mono text-xs text-muted-foreground"
                      title={ev.organization_id}
                    >
                      {ev.organization_id || <span className="italic">—</span>}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {ev.ip || "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>

        {detail && (
          <AuditDetailSheet event={detail} onClose={() => setDetail(null)} />
        )}

        {/* Pagination */}
        {!isLoading && !error && (hasPrev || hasMore) && (
          <div className="flex items-center justify-between text-sm text-muted-foreground">
            <span>Page {currentPage}</span>
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={!hasPrev}
                onClick={() => setOffset(Math.max(0, offset - AUDIT_PAGE_SIZE))}
              >
                <IconChevronLeft className="size-4" />
                Previous
              </Button>
              <Button
                variant="outline"
                size="sm"
                disabled={!hasMore}
                onClick={() => setOffset(offset + AUDIT_PAGE_SIZE)}
              >
                Next
                <IconChevronRight className="size-4" />
              </Button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
