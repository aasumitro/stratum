import { useTranslation } from "react-i18next"
import { useWebhookDeliveries } from "@/features/organization/hooks"

const DOT_TONE: Record<string, string> = {
  delivered: "bg-emerald-500",
  failed: "bg-destructive",
  pending: "bg-amber-500",
}

interface Props {
  organizationId: string
  webhookId: string
}

// List-page "sparkline" cue, built from real recent deliveries (most
// recent last) rather than a fabricated chart — the backend only exposes
// 24h/3-day aggregate counts (webhookHealth), not hour-bucketed history, so
// a literal line-chart sparkline isn't backed by real data yet.
export function WebhookSparkline({ organizationId, webhookId }: Props) {
  const { t } = useTranslation()
  const { data } = useWebhookDeliveries(organizationId, webhookId)
  const recent = (data?.data ?? []).slice(0, 30).reverse()

  if (!recent.length) return null

  // Color alone can't convey status to screen readers
  const counts = recent.reduce<Record<string, number>>((acc, d) => {
    acc[d.status] = (acc[d.status] ?? 0) + 1
    return acc
  }, {})
  const summary = t("organization.webhooks.sparklineSummary", {
    delivered: counts.delivered ?? 0,
    failed: counts.failed ?? 0,
    pending: counts.pending ?? 0,
  })

  const maxLatency = Math.max(...recent.map((d) => d.latency_ms ?? 0), 100)

  return (
    <div className="flex flex-col gap-1" title={summary}>
      <span className="sr-only">{summary}</span>
      <div className="flex items-end gap-[2px] h-6 w-full opacity-80" aria-hidden="true">
        {recent.map((d) => {
          const latency = d.latency_ms ?? 0
          const heightPercent = Math.max(15, Math.min(100, (latency / maxLatency) * 100))
          return (
            <div
              key={d.id}
              style={{ height: `${heightPercent}%` }}
              className={`w-[4px] sm:w-[5px] rounded-t-[1px] transition-all hover:brightness-110 ${DOT_TONE[d.status] ?? "bg-muted"}`}
              title={`${d.status} • ${latency}ms`}
            />
          )
        })}
      </div>
    </div>
  )
}
