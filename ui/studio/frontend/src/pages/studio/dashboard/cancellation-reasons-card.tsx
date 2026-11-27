import { useState } from "react"
import type {
  CancellationDetail,
  ReasonCount,
} from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { useCancellationReasons } from "@/hooks/use-dashboard"

const REASON_LABELS: Record<string, string> = {
  too_expensive: "Too expensive",
  missing_features: "Missing features",
  switching_provider: "Switching provider",
  no_longer_needed: "No longer needed",
  other: "Other",
}

const RANGES = [30, 90] as const

// The reasons owners picked when cancelling and the latest free-text details,
// so retention offers can follow what customers actually say. Read-only.
export function CancellationReasonsCard({ projectId }: { projectId: string }) {
  const [days, setDays] = useState<(typeof RANGES)[number]>(30)
  const { data, isLoading, error } = useCancellationReasons(projectId, days)
  const counts: ReasonCount[] = data?.counts ?? []
  const latest: CancellationDetail[] = data?.latest ?? []
  const total = counts.reduce((acc, c) => acc + c.count, 0)

  return (
    <Card className="p-5">
      <div className="mb-3 flex items-center justify-between">
        <span className="text-xs font-medium text-muted-foreground">
          Cancellation reasons
        </span>
        <div className="flex gap-1">
          {RANGES.map((r) => (
            <Button
              key={r}
              size="sm"
              variant={days === r ? "secondary" : "ghost"}
              onClick={() => setDays(r)}
            >
              {r}d
            </Button>
          ))}
        </div>
      </div>
      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : error ? (
        <p className="text-sm text-destructive">
          Could not load cancellation reasons.
        </p>
      ) : total === 0 ? (
        <p className="text-sm text-muted-foreground">
          No cancellations in the last {days} days.
        </p>
      ) : (
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            {counts.map((c) => (
              <div key={c.reason} className="flex items-center gap-3">
                <span className="flex-1 text-sm font-medium">
                  {REASON_LABELS[c.reason] ?? c.reason}
                </span>
                <span className="text-xs text-muted-foreground tabular-nums">
                  {Math.round((c.count / total) * 100)}%
                </span>
                <Badge variant="secondary" className="tabular-nums">
                  {c.count}
                </Badge>
              </div>
            ))}
          </div>
          {latest.length > 0 && (
            <div className="flex flex-col gap-2 border-t pt-3">
              <span className="text-xs font-medium text-muted-foreground">
                Latest details
              </span>
              {latest.map((d, i) => (
                <div key={`${d.at}-${i}`} className="text-sm">
                  <span className="text-xs text-muted-foreground">
                    {new Date(d.at).toLocaleDateString()} ·{" "}
                    {REASON_LABELS[d.reason] ?? d.reason}
                    {d.plan ? ` · ${d.plan}` : ""}
                  </span>
                  <p className="wrap-break-word whitespace-pre-wrap">
                    {d.details}
                  </p>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </Card>
  )
}
