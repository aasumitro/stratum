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
  const recent = (data?.data ?? []).slice(0, 15).reverse()

  if (!recent.length) return null

  // Color alone can't convey status to screen readers — the visual dots are
  // aria-hidden and a plain-text summary carries the same information.
  const counts = recent.reduce<Record<string, number>>((acc, d) => {
    acc[d.status] = (acc[d.status] ?? 0) + 1
    return acc
  }, {})
  const summary = t("organization.webhooks.sparklineSummary", {
    delivered: counts.delivered ?? 0,
    failed: counts.failed ?? 0,
    pending: counts.pending ?? 0,
  })

  return (
    <div className="flex items-center gap-0.5" title={summary}>
      <span className="sr-only">{summary}</span>
      <div className="flex items-center gap-0.5" aria-hidden="true">
        {recent.map((d) => (
          <span
            key={d.id}
            className={`size-1.5 rounded-full ${DOT_TONE[d.status] ?? "bg-muted"}`}
          />
        ))}
      </div>
    </div>
  )
}
