import { useTranslation } from "react-i18next"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { useUsage, useBillingFeatures } from "@/features/billing/hooks"
import { formatBytes } from "@/lib/format"
import { cn } from "@/lib/ui"

function formatDate(s: string) {
  return new Date(s).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })
}

function formatMetricValue(metric: string, value: number): string {
  if (metric === "storage_bytes") return formatBytes(value)
  return value.toLocaleString()
}

interface Props {
  organizationId: string
}

/**
 * Shows each metered feature's usage against its actual limit (plan +
 * any attached addon delta) — previously this rendered raw usage numbers
 * from GET /billing/usage with no limit context at all, which looked
 * "broken" next to the real meter (with a progress bar) the billing
 * overview page already shows. Now both surfaces read the same resolved
 * entitlement data (GET /billing/features), just at different granularity
 * (this page adds the last-recorded timestamp from GET /billing/usage).
 */
export function UsageMeters({ organizationId }: Props) {
  const { t } = useTranslation()
  const { data: entitlementsData, isLoading: entitlementsLoading } =
    useBillingFeatures(organizationId)
  const { data: usageData } = useUsage(organizationId)

  const metered = (entitlementsData?.data ?? []).filter(
    (e) => e.type === "metered"
  )
  const recordedAtByMetric = new Map(
    (usageData?.data ?? []).map((u) => [u.metric, u.recorded_at])
  )

  if (entitlementsLoading) {
    return (
      <div className="grid gap-3 sm:grid-cols-3">
        {[1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-24 w-full rounded-xl" />
        ))}
      </div>
    )
  }

  if (!metered.length) {
    return (
      <p className="text-sm text-muted-foreground">
        {t("billing.usage.noUsage")}
      </p>
    )
  }

  return (
    <div className="grid gap-3 sm:grid-cols-3">
      {metered.map((m) => {
        const current = m.current ?? 0
        const unlimited = m.limit === undefined || m.limit === -1
        const recordedAt = recordedAtByMetric.get(m.feature_id)
        const hasAddonDelta = (m.addon_delta ?? 0) > 0
        return (
          <Card key={m.feature_id}>
            <CardHeader className="pb-1">
              <CardTitle className="text-sm">
                {t(`billing.usage.metric.${m.feature_id}`, {
                  defaultValue: m.name,
                })}
              </CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col gap-2">
              <p className="text-2xl font-bold">
                {formatMetricValue(m.feature_id, current)}
                {!unlimited && (
                  <span className="ml-1 text-sm font-normal text-muted-foreground">
                    / {formatMetricValue(m.feature_id, m.limit ?? 0)}
                  </span>
                )}
                {unlimited && (
                  <span className="ml-1 text-sm font-normal text-muted-foreground">
                    ({t("billing.usage.unlimited")})
                  </span>
                )}
              </p>
              {/* "6 / 8 · 3 plan + 5 addon" split, so "why is my limit
                  8?" never becomes a support ticket. */}
              {!unlimited && hasAddonDelta && (
                <span className="w-fit rounded-full bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground">
                  {t("billing.usage.planAddonSplit", {
                    plan: formatMetricValue(m.feature_id, m.plan_limit ?? 0),
                    addon: formatMetricValue(m.feature_id, m.addon_delta ?? 0),
                  })}
                </span>
              )}
              {!unlimited && m.limit ? (
                <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
                  <div
                    // Usage bars amber >=80%, red >=95%.
                    className={cn(
                      "h-full",
                      current / m.limit >= 0.95
                        ? "bg-destructive"
                        : current / m.limit >= 0.8
                          ? "bg-amber-500"
                          : "bg-primary"
                    )}
                    style={{
                      width: `${Math.min(100, (current / m.limit) * 100)}%`,
                    }}
                  />
                </div>
              ) : null}
              {recordedAt && (
                <p className="text-xs text-muted-foreground">
                  {t("billing.usage.recordedAt")} {formatDate(recordedAt)}
                </p>
              )}
            </CardContent>
          </Card>
        )
      })}
    </div>
  )
}
