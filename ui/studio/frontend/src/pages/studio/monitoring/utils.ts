import type { ComponentStatus } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"

const STATUS_COLORS = {
  ok:       { dot: "bg-green-500",  badge: "bg-green-500/15 text-green-700 dark:text-green-400" },
  degraded: { dot: "bg-amber-500",  badge: "bg-amber-500/15 text-amber-700 dark:text-amber-400" },
  down:     { dot: "bg-red-500",    badge: "bg-red-500/15 text-red-700 dark:text-red-400" },
} as const

export function statusStyle(status: string) {
  return STATUS_COLORS[status as keyof typeof STATUS_COLORS] ?? {
    dot: "bg-muted-foreground/40",
    badge: "bg-muted text-muted-foreground",
  }
}

export const INTERVALS = [
  { label: "30s", value: 30  },
  { label: "1m",  value: 60  },
  { label: "2m",  value: 120 },
  { label: "5m",  value: 300 },
]

export function formatLatency(ms: number | null | undefined): string {
  if (ms == null || ms < 0) return "—"
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

export function timeAgo(iso: string): string {
  const diff = Math.floor((Date.now() - new Date(iso).getTime()) / 1000)
  if (diff < 60) return `${diff}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  return `${Math.floor(diff / 3600)}h ago`
}

export function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d}d ${h}h ${m}m`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m ${seconds % 60}s`
}

export function hasComponents(c: ComponentStatus): boolean {
  return !!(c.postgres || c.redis || c.rabbitmq)
}
