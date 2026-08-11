import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/ui"

export type StatusFilter = "" | "active" | "suspended" | "deleted"

export function statusBadge(status: string) {
  const map: Record<string, string> = {
    active:
      "bg-green-500/10 text-green-700 dark:text-green-400 border-green-500/20",
    suspended:
      "bg-amber-500/10 text-amber-700 dark:text-amber-400 border-amber-500/20",
    deleted: "bg-red-500/10 text-red-700 dark:text-red-400 border-red-500/20",
  }
  return (
    <span
      className={cn(
        "inline-flex items-center rounded border px-2 py-0.5 text-xs font-medium",
        map[status] ?? "bg-muted text-muted-foreground"
      )}
    >
      {status}
    </span>
  )
}

export function billingBadge(billing: string) {
  if (!billing) return <span className="text-xs text-muted-foreground">—</span>
  return (
    <Badge variant="secondary" className="text-xs">
      {billing}
    </Badge>
  )
}
