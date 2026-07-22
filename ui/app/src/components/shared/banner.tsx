import type { ReactNode } from "react"
import {
  IconInfoCircle,
  IconAlertTriangle,
  IconAlertCircle,
} from "@tabler/icons-react"
import { cn } from "@/lib/ui"

export type BannerSeverity = "info" | "warning" | "critical"

const SEVERITY_RANK: Record<BannerSeverity, number> = {
  info: 0,
  warning: 1,
  critical: 2,
}

const SEVERITY_STYLE: Record<BannerSeverity, string> = {
  info: "border-blue-500/20 bg-blue-500/10 text-blue-700 dark:text-blue-400",
  warning:
    "border-amber-500/20 bg-amber-500/10 text-amber-700 dark:text-amber-400",
  critical: "border-destructive/20 bg-destructive/10 text-destructive",
}

const SEVERITY_ICON: Record<BannerSeverity, typeof IconInfoCircle> = {
  info: IconInfoCircle,
  warning: IconAlertTriangle,
  critical: IconAlertCircle,
}

export interface BannerItem {
  id: string
  severity: BannerSeverity
  message: ReactNode
  action?: ReactNode
}

/**
 * Global banner slot under the top bar: renders only the single
 * highest-severity item from the list — never stacks multiple banners.
 * Callers collect every candidate banner (suspended, pending invoice,
 * dunning stage, etc.) into one array and hand it here.
 */
export function Banner({ banners }: { banners: BannerItem[] }) {
  if (banners.length === 0) return null

  const top = [...banners].sort(
    (a, b) => SEVERITY_RANK[b.severity] - SEVERITY_RANK[a.severity]
  )[0]
  const Icon = SEVERITY_ICON[top.severity]

  return (
    <div
      className={cn(
        "flex items-center gap-2 rounded-xl border px-4 py-2.5 text-sm",
        SEVERITY_STYLE[top.severity]
      )}
    >
      <Icon className="size-4 shrink-0" />
      <span className="flex-1">{top.message}</span>
      {top.action}
    </div>
  )
}
