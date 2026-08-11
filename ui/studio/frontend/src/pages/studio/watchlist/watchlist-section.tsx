import { IconCircleCheck } from "@tabler/icons-react"
import type { WatchlistItem } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { cn } from "@/lib/ui"
import { formatDate } from "../components/table-utils"
import { urgencyChip } from "./utils"

interface SectionProps {
  title: string
  subtitle: string
  icon: React.ReactNode
  items: WatchlistItem[]
  sectionType: "trial" | "past_due" | "renewal" | "cancellation"
  dateLabel: string
  dateKey: "trial_end" | "period_end"
  urgent?: boolean
}

export function WatchlistSection({
  title,
  subtitle,
  icon,
  items,
  sectionType,
  dateLabel,
  dateKey,
  urgent,
}: SectionProps) {
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <span className={urgent ? "text-red-500" : "text-muted-foreground"}>
          {icon}
        </span>
        <div>
          <h2 className="text-sm font-semibold">{title}</h2>
          <p className="text-xs text-muted-foreground">{subtitle}</p>
        </div>
        {items.length > 0 && (
          <span
            className={cn(
              "ml-auto inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium",
              urgent
                ? "bg-red-500/10 text-red-700 dark:text-red-400"
                : "bg-muted text-muted-foreground"
            )}
          >
            {items.length}
          </span>
        )}
      </div>

      {items.length === 0 ? (
        <div className="flex items-center gap-2 rounded-lg border border-dashed px-4 py-4 text-muted-foreground">
          <IconCircleCheck className="size-4 opacity-50" />
          <span className="text-xs">All clear</span>
        </div>
      ) : (
        <div className="divide-y overflow-hidden rounded-lg border">
          {items.map((item) => (
            <div
              key={item.organization_id}
              className="flex items-center gap-4 bg-card px-4 py-3 transition-colors hover:bg-muted/30"
            >
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">
                  {item.organization_name}
                </p>
                <p className="font-mono text-xs text-muted-foreground">
                  /{item.organization_slug}
                </p>
              </div>
              <div className="shrink-0 text-xs text-muted-foreground">
                {item.plan_name || "—"}
              </div>
              <div
                className="w-28 shrink-0 text-right text-xs text-muted-foreground"
                title={dateLabel}
              >
                {formatDate(item[dateKey])}
              </div>
              <div className="flex w-20 shrink-0 justify-end">
                {urgencyChip(item.days_remaining, sectionType)}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
