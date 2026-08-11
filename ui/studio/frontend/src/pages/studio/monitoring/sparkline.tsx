import type { MonitorLog } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { formatLatency, statusStyle, timeAgo } from "./utils"

export function Sparkline({ history }: { history: MonitorLog[] }) {
  const sorted = [...history].reverse()

  return (
    <div className="flex flex-col gap-2">
      <span className="text-xs font-medium text-muted-foreground">
        Last {sorted.length} checks
      </span>
      <div className="flex h-8 items-end gap-0.5">
        {sorted.map((log) => {
          const style = statusStyle(log.status)
          const height =
            log.latency_ms != null && log.latency_ms > 0
              ? Math.min(100, Math.max(20, (log.latency_ms / 1000) * 30))
              : 30
          return (
            <div
              key={log.id}
              className={`max-w-2.5 flex-1 rounded-sm ${style.dot} opacity-80 transition-opacity hover:opacity-100`}
              style={{ height: `${height}%` }}
              title={`${log.status} · ${formatLatency(log.latency_ms)} · ${timeAgo(log.checked_at)}`}
            />
          )
        })}
      </div>
      <div className="flex items-center gap-3 text-xs text-muted-foreground">
        <span className="flex items-center gap-1">
          <span className="inline-block size-2 rounded-full bg-green-500" /> ok
        </span>
        <span className="flex items-center gap-1">
          <span className="inline-block size-2 rounded-full bg-amber-500" />{" "}
          degraded (&ge;500ms)
        </span>
        <span className="flex items-center gap-1">
          <span className="inline-block size-2 rounded-full bg-red-500" /> down
        </span>
      </div>
    </div>
  )
}
