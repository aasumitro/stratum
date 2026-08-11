import { cn } from "@/lib/ui"

export function urgencyChip(
  days: number,
  sectionType: "trial" | "past_due" | "renewal" | "cancellation"
) {
  if (sectionType === "past_due") {
    return (
      <span className="inline-flex items-center rounded border border-red-500/20 bg-red-500/10 px-2 py-0.5 text-xs font-medium text-red-700 dark:text-red-400">
        overdue
      </span>
    )
  }
  if (sectionType === "cancellation") {
    return (
      <span className="inline-flex items-center rounded border border-border bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
        {days}d left
      </span>
    )
  }
  // trial or renewal: color by urgency
  const color =
    days <= 2
      ? "bg-red-500/10 text-red-700 dark:text-red-400 border-red-500/20"
      : "bg-amber-500/10 text-amber-700 dark:text-amber-400 border-amber-500/20"
  return (
    <span
      className={cn(
        "inline-flex items-center rounded border px-2 py-0.5 text-xs font-medium",
        color
      )}
    >
      {days}d
    </span>
  )
}
