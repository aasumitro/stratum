import { useState } from "react"
import { useParams } from "@tanstack/react-router"
import {
  IconChevronLeft,
  IconChevronRight,
  IconClipboard,
  IconHistory,
} from "@tabler/icons-react"
import { toast } from "sonner"
import type { OperatorLogEntry } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import {
  OPERATOR_LOG_PAGE_SIZE,
  useOperatorLog,
  useOperatorLogCount,
} from "@/hooks/use-operator-log"
import { Button } from "@/components/ui/button"
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
import { ACTION_STYLE, actionLabel, timeAgo } from "./utils"
import { EntryDetailSheet } from "./entry-detail-sheet"

export function OperatorLogPage() {
  const { projectId } = useParams({ from: "/studio/$projectId/operator-log" })

  const [offset, setOffset] = useState(0)
  const [detail, setDetail] = useState<OperatorLogEntry | null>(null)

  const {
    data: entries,
    isLoading,
    error,
    refetch,
  } = useOperatorLog(projectId, OPERATOR_LOG_PAGE_SIZE, offset)
  const { data: total } = useOperatorLogCount(projectId)

  const hasMore = (entries?.length ?? 0) === OPERATOR_LOG_PAGE_SIZE
  const hasPrev = offset > 0
  const currentPage = Math.floor(offset / OPERATOR_LOG_PAGE_SIZE) + 1

  return (
    <div className="flex min-h-full flex-col">
      <header className="flex items-center gap-3 border-b px-6 py-4">
        <IconHistory className="size-4 text-muted-foreground" />
        <div className="flex flex-col gap-0.5">
          <h1 className="text-sm font-semibold">Operator Log</h1>
          <p className="text-xs text-muted-foreground">
            Audit trail of support actions taken in Studio
            {total !== undefined && (
              <span className="ml-1 font-medium text-foreground">
                ({total.toLocaleString()})
              </span>
            )}
          </p>
        </div>
      </header>

      <div className="space-y-5 p-6">
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
              <span>Failed to load operator log</span>
              <Button variant="outline" size="sm" onClick={() => refetch()}>
                Try again
              </Button>
            </div>
          ) : !entries || entries.length === 0 ? (
            <div className="flex flex-col items-center gap-2 py-12 text-muted-foreground">
              <IconHistory className="size-8 opacity-30" />
              <span className="text-sm">No operator actions recorded yet</span>
              <span className="max-w-xs text-center text-xs">
                Actions from the Support panel (extend trial, change plan, void
                invoice, etc.) will appear here.
              </span>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Action</TableHead>
                  <TableHead>Target</TableHead>
                  <TableHead>Detail</TableHead>
                  <TableHead>Time</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {entries.map((entry) => (
                  <TableRow
                    key={entry.id}
                    className="cursor-pointer hover:bg-muted/50"
                    onClick={() => setDetail(entry)}
                  >
                    <TableCell>
                      <span
                        className={cn(
                          "inline-flex items-center rounded border px-2 py-0.5 text-xs font-medium capitalize",
                          ACTION_STYLE[entry.action] ??
                            "border-border bg-muted text-muted-foreground"
                        )}
                      >
                        {actionLabel(entry.action)}
                      </span>
                    </TableCell>
                    <TableCell className="max-w-45 font-mono text-xs text-muted-foreground">
                      <div className="flex items-center gap-1.5">
                        <span className="truncate" title={entry.target_id}>
                          {entry.target_id}
                        </span>
                        <button
                          type="button"
                          className="shrink-0 text-muted-foreground hover:text-foreground"
                          title="Copy target ID"
                          onClick={(e) => {
                            e.stopPropagation()
                            void navigator.clipboard
                              .writeText(entry.target_id)
                              .then(
                                () => toast.success("ID copied"),
                                () => toast.error("Copy failed")
                              )
                          }}
                        >
                          <IconClipboard className="size-3" />
                        </button>
                      </div>
                    </TableCell>
                    <TableCell className="max-w-50 truncate text-xs text-muted-foreground">
                      {entry.detail || <span className="italic">—</span>}
                    </TableCell>
                    <TableCell
                      className="text-xs whitespace-nowrap text-muted-foreground"
                      title={new Date(entry.performed_at).toLocaleString()}
                    >
                      {timeAgo(entry.performed_at)}
                    </TableCell>
                    <TableCell />
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>

        {!isLoading && !error && (hasPrev || hasMore) && (
          <div className="flex items-center justify-between text-sm text-muted-foreground">
            <span>Page {currentPage}</span>
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={!hasPrev}
                onClick={() =>
                  setOffset(Math.max(0, offset - OPERATOR_LOG_PAGE_SIZE))
                }
              >
                <IconChevronLeft className="size-4" />
                Previous
              </Button>
              <Button
                variant="outline"
                size="sm"
                disabled={!hasMore}
                onClick={() => setOffset(offset + OPERATOR_LOG_PAGE_SIZE)}
              >
                Next
                <IconChevronRight className="size-4" />
              </Button>
            </div>
          </div>
        )}
      </div>

      {detail && (
        <EntryDetailSheet entry={detail} onClose={() => setDetail(null)} />
      )}
    </div>
  )
}
