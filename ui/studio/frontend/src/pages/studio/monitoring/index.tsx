import { useState } from "react"
import { useParams } from "@tanstack/react-router"
import {
  IconActivity,
  IconPlayerPlay,
  IconPlayerStop,
  IconRefresh,
} from "@tabler/icons-react"
import type { MonitorLog } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import {
  useCheckHealth,
  useHistory,
  useIsPolling,
  useLastStatus,
  useStartPoller,
  useStopPoller,
} from "@/hooks/use-monitor"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { formatBytes } from "../components/table-utils"
import {
  formatLatency,
  hasComponents,
  INTERVALS,
  statusStyle,
  timeAgo,
} from "./utils"
import { ComponentDots } from "./component-dots"
import { StatsGrid } from "./stats-grid"
import { Sparkline } from "./sparkline"

export function MonitoringPage() {
  const { projectId } = useParams({ from: "/studio/$projectId" })
  const [interval, setInterval] = useState(60)
  const [historyLimit, setHistoryLimit] = useState(10)

  const { data: lastStatus } = useLastStatus(projectId, 10_000)
  const {
    data: history,
    isLoading: historyLoading,
    error: historyError,
    refetch: refetchHistory,
  } = useHistory(projectId, historyLimit)
  const { data: isPolling } = useIsPolling(projectId)

  const checkHealth = useCheckHealth(projectId)
  const startPoller = useStartPoller(projectId)
  const stopPoller = useStopPoller(projectId)

  const uptime =
    history && history.length > 0
      ? Math.round(
          (history.filter((l) => l.status === "ok").length / history.length) *
            100
        )
      : null

  return (
    <div className="flex min-h-full flex-col">
      {/* Header */}
      <header className="flex items-center justify-between border-b px-6 py-4">
        <div className="flex items-center gap-3">
          <IconActivity className="size-4 text-muted-foreground" />
          <div className="flex flex-col gap-0.5">
            <h1 className="text-sm font-semibold">Monitoring</h1>
            <p className="text-xs text-muted-foreground">
              Readiness + runtime stats via{" "}
              <code className="text-[10px]">/health/ready</code> &amp;{" "}
              <code className="text-[10px]">/health/stats</code>
            </p>
          </div>
        </div>

        {/* Poller controls */}
        <div className="flex flex-col items-end gap-1">
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => checkHealth.mutate()}
              disabled={checkHealth.isPending}
            >
              <IconRefresh
                className={`size-4 ${checkHealth.isPending ? "animate-spin" : ""}`}
              />
              Check now
            </Button>

            <select
              value={interval}
              onChange={(e) => setInterval(Number(e.target.value))}
              className="h-8 rounded-md border border-input bg-background px-2 text-xs focus-visible:ring-1 focus-visible:ring-ring focus-visible:outline-hidden"
            >
              {INTERVALS.map((i) => (
                <option key={i.value} value={i.value}>
                  {i.label}
                </option>
              ))}
            </select>

            {isPolling ? (
              <Button
                variant="outline"
                size="sm"
                onClick={() => stopPoller.mutate()}
                disabled={stopPoller.isPending}
                className="text-destructive hover:text-destructive"
              >
                <IconPlayerStop className="size-4" />
                Stop
              </Button>
            ) : (
              <Button
                size="sm"
                onClick={() => startPoller.mutate(interval)}
                disabled={startPoller.isPending}
              >
                <IconPlayerPlay className="size-4" />
                Start polling
              </Button>
            )}
          </div>
          {checkHealth.data?.message && (
            <p
              className="max-w-xs truncate text-right font-mono text-[10px] text-destructive"
              title={checkHealth.data.message}
            >
              {checkHealth.data.message}
            </p>
          )}
        </div>
      </header>

      <div className="flex flex-1 flex-col gap-6 overflow-y-auto p-6">
        {/* Current status card */}
        <div className="flex items-start gap-4 rounded-lg border bg-card p-5">
          <div className="flex flex-1 flex-col gap-3">
            <div className="flex items-center gap-3">
              {lastStatus ? (
                <>
                  <span
                    className={`size-3 rounded-full ${statusStyle(lastStatus.status).dot}`}
                  />
                  <span
                    className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium ${statusStyle(lastStatus.status).badge}`}
                  >
                    {lastStatus.status}
                  </span>
                  <span className="text-sm font-medium tabular-nums">
                    {formatLatency(lastStatus.latency_ms)}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {timeAgo(lastStatus.checked_at)}
                  </span>
                </>
              ) : (
                <>
                  <span className="size-3 rounded-full bg-muted-foreground/30" />
                  <span className="text-sm text-muted-foreground">
                    No checks yet — click "Check now" or start polling
                  </span>
                </>
              )}
            </div>

            {lastStatus && hasComponents(lastStatus.components) && (
              <ComponentDots components={lastStatus.components} />
            )}

            {isPolling && (
              <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                <span className="size-1.5 animate-pulse rounded-full bg-green-500" />
                Polling every{" "}
                {INTERVALS.find((i) => i.value === interval)?.label ??
                  `${interval}s`}
              </div>
            )}
          </div>

          {uptime !== null && (
            <div className="flex flex-col items-end gap-0.5">
              <span className="text-2xl font-semibold tabular-nums">
                {uptime}%
              </span>
              <span className="text-xs text-muted-foreground">
                uptime (last {history?.length})
              </span>
            </div>
          )}
        </div>

        {/* Runtime stats */}
        {lastStatus?.stats && (
          <div className="rounded-lg border bg-card p-5">
            <StatsGrid stats={lastStatus.stats} />
          </div>
        )}

        {/* Sparkline */}
        {history && history.length > 0 && (
          <div className="rounded-lg border bg-card p-5">
            <Sparkline history={history} />
          </div>
        )}

        {/* History table */}
        <div className="overflow-hidden rounded-lg border bg-card">
          <div className="flex items-center justify-between border-b px-5 py-3">
            <h2 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
              Recent checks
            </h2>
            <select
              value={historyLimit}
              onChange={(e) => setHistoryLimit(Number(e.target.value))}
              className="h-7 rounded-md border border-input bg-background px-2 text-xs focus-visible:ring-1 focus-visible:ring-ring focus-visible:outline-hidden"
            >
              <option value={10}>Last 10</option>
              <option value={20}>Last 20</option>
              <option value={50}>Last 50</option>
              <option value={100}>Last 100</option>
              <option value={200}>Last 200</option>
            </select>
          </div>

          {historyLoading ? (
            <div className="flex animate-pulse flex-col gap-3 p-4">
              {Array.from({ length: 5 }).map((_, i) => (
                <div key={i} className="h-4 rounded bg-muted" />
              ))}
            </div>
          ) : historyError ? (
            <div className="flex flex-col items-center justify-center gap-2 py-8 text-center">
              <p className="text-sm font-medium text-destructive">
                Failed to load history
              </p>
              <p className="text-xs text-muted-foreground">
                {String(historyError)}
              </p>
              <Button
                variant="outline"
                size="sm"
                onClick={() => refetchHistory()}
              >
                <IconRefresh className="size-4" />
                Try again
              </Button>
            </div>
          ) : !history || history.length === 0 ? (
            <div className="flex items-center justify-center py-12 text-sm text-muted-foreground">
              No history yet
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-24">Status</TableHead>
                  <TableHead className="w-28">Latency</TableHead>
                  <TableHead className="w-40">Components</TableHead>
                  <TableHead className="w-32">Goroutines</TableHead>
                  <TableHead className="w-28">Heap</TableHead>
                  <TableHead>Checked at</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {history.map((log: MonitorLog) => {
                  const style = statusStyle(log.status)
                  return (
                    <TableRow key={log.id}>
                      <TableCell>
                        <span
                          className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium ${style.badge}`}
                        >
                          <span
                            className={`size-1.5 rounded-full ${style.dot}`}
                          />
                          {log.status}
                        </span>
                      </TableCell>
                      <TableCell className="font-mono text-xs tabular-nums">
                        {formatLatency(log.latency_ms)}
                      </TableCell>
                      <TableCell>
                        {hasComponents(log.components) ? (
                          <ComponentDots components={log.components} />
                        ) : (
                          <span className="text-xs text-muted-foreground">
                            —
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="text-xs tabular-nums">
                        {log.stats ? (
                          String(log.stats.goroutines)
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </TableCell>
                      <TableCell className="text-xs tabular-nums">
                        {log.stats ? (
                          formatBytes(log.stats.memory.heap_alloc_bytes)
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </TableCell>
                      <TableCell className="text-xs text-muted-foreground">
                        <span title={log.checked_at}>
                          {timeAgo(log.checked_at)}
                        </span>
                        <span className="ml-2 opacity-50">
                          {new Date(log.checked_at).toLocaleTimeString()}
                        </span>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </div>
      </div>
    </div>
  )
}
