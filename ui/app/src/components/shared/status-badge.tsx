import { useTranslation } from "react-i18next"
import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/ui"

type StatusTone = "neutral" | "success" | "warning" | "danger" | "info"

const TONE_CLASS: Record<StatusTone, string> = {
  neutral: "bg-muted text-muted-foreground",
  success: "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
  warning: "bg-amber-500/10 text-amber-600 dark:text-amber-400",
  danger: "bg-destructive/10 text-destructive",
  info: "bg-blue-500/10 text-blue-600 dark:text-blue-400",
}

// Covers organization / subscription / invoice / payment-link / webhook /
// async-task statuses — one map instead of a ROLE_BADGE/STATUS_BADGE const
// re-declared per feature file.
const STATUS_TONE: Record<string, StatusTone> = {
  active: "success",
  suspended: "warning",
  deleted: "danger",
  trialing: "info",
  cancelled: "neutral",
  past_due: "warning",
  expired: "danger",
  pending: "warning",
  paid: "success",
  failed: "danger",
  void: "neutral",
  enabled: "success",
  disabled: "neutral",
  processing: "info",
  delivered: "success",
  completed: "success",
}

interface StatusBadgeProps {
  status: string
  label?: string
  className?: string
}

export function StatusBadge({ status, label, className }: StatusBadgeProps) {
  const { t } = useTranslation()
  const tone = STATUS_TONE[status] ?? "neutral"
  return (
    <Badge
      variant="outline"
      className={cn(
        "border-transparent capitalize",
        TONE_CLASS[tone],
        className
      )}
    >
      {label ??
        t(`common.status.${status}`, {
          defaultValue: status.replace(/_/g, " "),
        })}
    </Badge>
  )
}
