import type { MonitorLog } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { formatLatency, statusStyle, timeAgo } from "./utils"

export function Sparkline({ history }: { history: MonitorLog[] }) {
  const sorted = [...history].reverse()

  return (
    <div className="flex flex-col gap-2">
      <span className="text-xs text-muted-foreground font-medium">
        Last {sorted.length} checks
      </span>
      <div className="flex items-end gap-0.5 h-8">
        {sorted.map((log) => {
          const style = statusStyle(log.status)
          const height = log.latency_ms != null && log.latency_ms > 0
            ? Math.min(100, Math.max(20, (log.latency_ms / 1000) * 30))
            : 30
          return (
            <div
              key={log.id}
              className={`flex-1 max-w-[10px] rounded-sm ${style.dot} opacity-80 hover:opacity-100 transition-opacity`}
              style={{ height: `${height}%` }}
              title={`${log.status} · ${formatLatency(log.latency_ms)} · ${timeAgo(log.checked_at)}`}
            />
          )
        })}
      </div>
      <div className="flex items-center gap-3 text-xs text-muted-foreground">
        <span className="flex items-center gap-1">
          <span className="size-2 rounded-full bg-green-500 inline-block" /> ok
        </span>
        <span className="flex items-center gap-1">
          <span className="size-2 rounded-full bg-amber-500 inline-block" /> degraded (&ge;500ms)
        </span>
        <span className="flex items-center gap-1">
          <span className="size-2 rounded-full bg-red-500 inline-block" /> down
        </span>
      </div>
    </div>
  )
}
