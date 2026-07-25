import { useTranslation } from "react-i18next"
import { IconUsers, IconDatabase, IconChartBar } from "@tabler/icons-react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { useUsage, useBillingFeatures } from "@/features/billing/hooks"
import { formatBytes } from "@/lib/format"
import { cn } from "@/lib/ui"

const METRIC_ICON: Record<string, typeof IconUsers> = {
  members: IconUsers,
  storage: IconDatabase,
}

// The `storage` feature's usage is recorded under metric key
// "storage_bytes" (billing.features.metric_key), not its own feature id —
// every other metered feature's id and metric_key happen to be the same
// string, so this is the one case that needs an explicit bridge.
const FEATURE_TO_METRIC_KEY: Record<string, string> = {
  storage: "storage_bytes",
}

function formatDate(s: string) {
  return new Date(s).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })
}

function formatMetricValue(featureId: string, value: number): string {
  if (featureId === "storage") return formatBytes(value)
  return value.toLocaleString()
}

interface Props {
  organizationId: string
}

/**
 * Shows each metered feature's usage against its actual limit (plan +
 * any attached addon delta). Reads the resolved entitlement data
 * (GET /billing/features) and adds the last-recorded timestamp
 * (GET /billing/usage).
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

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("billing.usage.title")}</CardTitle>
      </CardHeader>
      <CardContent>
        {entitlementsLoading ? (
          <div className="flex flex-col gap-6">
            {[1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-16 w-full" />
            ))}
          </div>
        ) : !metered.length ? (
          <p className="text-sm text-muted-foreground">
            {t("billing.usage.noUsage")}
          </p>
        ) : (
          <div className="flex flex-col gap-6">
            {metered.map((m) => {
              const current = m.current ?? 0
              const unlimited = m.limit === undefined || m.limit === -1
              const recordedAt = recordedAtByMetric.get(
                FEATURE_TO_METRIC_KEY[m.feature_id] ?? m.feature_id
              )
              const hasAddonDelta = (m.addon_delta ?? 0) > 0
              const Icon = METRIC_ICON[m.feature_id] ?? IconChartBar
              return (
                <div
                  key={m.feature_id}
                  className="flex flex-col gap-2 border-b border-border pb-6 last:border-0 last:pb-0"
                >
                  <div className="flex items-start justify-between gap-2">
                    <div className="flex items-center gap-3">
                      <div className="rounded-lg bg-primary/10 p-2 text-primary">
                        <Icon className="size-5" />
                      </div>
                      <div>
                        <p className="font-medium">
                          {t(`billing.usage.metric.${m.feature_id}`, {
                            defaultValue: m.name,
                          })}
                        </p>
                        {recordedAt && (
                          <p className="text-sm text-muted-foreground">
                            {t("billing.usage.recordedAt")}{" "}
                            {formatDate(recordedAt)}
                          </p>
                        )}
                      </div>
                    </div>
                    <div className="text-right">
                      <p className="font-semibold">
                        {formatMetricValue(m.feature_id, current)}
                        {!unlimited && (
                          <span className="ml-1 font-normal text-muted-foreground">
                            / {formatMetricValue(m.feature_id, m.limit ?? 0)}
                          </span>
                        )}
                        {unlimited && (
                          <span className="ml-1 font-normal text-muted-foreground">
                            ({t("billing.usage.unlimited")})
                          </span>
                        )}
                      </p>
                      {/* "6 / 8 · 3 plan + 5 addon" split, so "why is my
                          limit 8?" never becomes a support ticket. */}
                      {!unlimited && hasAddonDelta && (
                        <span className="mt-1 inline-block w-fit rounded-full bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground">
                          {t("billing.usage.planAddonSplit", {
                            plan: formatMetricValue(
                              m.feature_id,
                              m.plan_limit ?? 0
                            ),
                            addon: formatMetricValue(
                              m.feature_id,
                              m.addon_delta ?? 0
                            ),
                          })}
                        </span>
                      )}
                    </div>
                  </div>
                  {!unlimited && m.limit ? (
                    <div className="flex flex-col gap-1">
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
                      <p className="text-xs text-muted-foreground">
                        {t("billing.usage.percentUsed", {
                          percent: ((current / m.limit) * 100).toFixed(1),
                        })}
                      </p>
                    </div>
                  ) : null}
                </div>
              )
            })}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
