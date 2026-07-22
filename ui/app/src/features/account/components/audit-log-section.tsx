import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconDownload } from "@tabler/icons-react"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Skeleton } from "@/components/ui/skeleton"
import { useAuditLog } from "@/features/account/hooks"
import { useCursorAccumulator } from "@/lib/api/use-cursor-accumulator"
import { downloadFile } from "@/lib/api/download"
import { API } from "@/lib/api/path"

const METHOD_BADGE: Record<string, string> = {
  POST: "bg-blue-500/10 text-blue-600",
  PATCH: "bg-amber-500/10 text-amber-600",
  DELETE: "bg-destructive/10 text-destructive",
  PUT: "bg-purple-500/10 text-purple-600",
}

// end-of-day so a "to" date includes every event that happened on that day,
// not just up to midnight.
function endOfDayISO(dateStr: string): string {
  const d = new Date(dateStr)
  d.setHours(23, 59, 59, 999)
  return d.toISOString()
}

export function AuditLogSection() {
  const { t } = useTranslation()
  const [fromDate, setFromDate] = useState("")
  const [toDate, setToDate] = useState("")
  const [cursor, setCursor] = useState<string | undefined>(undefined)

  const from = fromDate ? new Date(fromDate).toISOString() : undefined
  const to = toDate ? endOfDayISO(toDate) : undefined

  const { data, isLoading, isFetching } = useAuditLog(cursor, 20, from, to)
  const { items, nextCursor } = useCursorAccumulator(data, cursor)

  // The date range scopes both the visible table and the CSV export to the
  // same window — resetting the cursor here mirrors the notifications page's
  // org-filter pattern (a changed filter needs a fresh first page, not more
  // pages appended onto the old filter's results).
  function handleFromChange(value: string) {
    setFromDate(value)
    setCursor(undefined)
  }

  function handleToChange(value: string) {
    setToDate(value)
    setCursor(undefined)
  }

  async function exportCSV() {
    let url = API.me("audit-log", "export")
    const params = new URLSearchParams()
    if (from) params.set("from", from)
    if (to) params.set("to", to)
    if (params.size) url += "?" + params.toString()
    await downloadFile(url, "audit-log.csv", "blob")
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h2 className="text-xl font-semibold">
            {t("account.auditLog.title")}
          </h2>
          <p className="mt-1 text-sm text-muted-foreground">
            {t("account.auditLog.description")}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <input
            type="date"
            value={fromDate}
            onChange={(e) => handleFromChange(e.target.value)}
            className="h-8 rounded-md border border-input bg-background px-2 text-xs"
            aria-label={t("account.auditLog.fromDate")}
          />
          <span className="text-xs text-muted-foreground">—</span>
          <input
            type="date"
            value={toDate}
            onChange={(e) => handleToChange(e.target.value)}
            className="h-8 rounded-md border border-input bg-background px-2 text-xs"
            aria-label={t("account.auditLog.toDate")}
          />
          <Button variant="outline" size="sm" onClick={() => void exportCSV()}>
            <IconDownload data-icon="inline-start" />
            {t("account.auditLog.exportCsv")}
          </Button>
        </div>
      </div>

      <div className="rounded-xl border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("account.auditLog.methodCol")}</TableHead>
              <TableHead>{t("account.auditLog.resourceCol")}</TableHead>
              <TableHead>{t("account.auditLog.statusCol")}</TableHead>
              <TableHead>{t("account.auditLog.whenCol")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {isLoading && items.length === 0 ? (
              Array.from({ length: 5 }).map((_, i) => (
                <TableRow key={i}>
                  {Array.from({ length: 4 }).map((__, j) => (
                    <TableCell key={j}>
                      <Skeleton className="h-4 w-24" />
                    </TableCell>
                  ))}
                </TableRow>
              ))
            ) : items.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={4}
                  className="py-8 text-center text-sm text-muted-foreground"
                >
                  {t("account.auditLog.empty")}
                </TableCell>
              </TableRow>
            ) : (
              items.map((event) => (
                <TableRow key={event.id}>
                  <TableCell>
                    <span
                      className={`rounded-full px-2 py-0.5 text-xs font-medium ${METHOD_BADGE[event.action] ?? "bg-muted text-muted-foreground"}`}
                    >
                      {event.action}
                    </span>
                  </TableCell>
                  <TableCell className="max-w-64 truncate font-mono text-xs text-muted-foreground">
                    {event.resource}
                  </TableCell>
                  <TableCell>
                    <Badge
                      variant="outline"
                      className={
                        event.status_code < 400
                          ? "text-emerald-600"
                          : "text-destructive"
                      }
                    >
                      {event.status_code}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-xs whitespace-nowrap text-muted-foreground">
                    {new Date(event.created_at).toLocaleString()}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      {nextCursor && (
        <Button
          variant="outline"
          size="sm"
          className="w-full"
          disabled={isFetching}
          onClick={() => setCursor(nextCursor)}
        >
          {isFetching ? t("common.loading") : t("common.loadMore")}
        </Button>
      )}
    </div>
  )
}
