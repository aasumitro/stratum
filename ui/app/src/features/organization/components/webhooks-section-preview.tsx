import { useNavigate } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { IconWebhook } from "@tabler/icons-react"
import { Skeleton } from "@/components/ui/skeleton"
import { EmptyState } from "@/components/shared/empty-state"
import { FeatureGateCard } from "@/components/shared/feature-gate-card"
import { SettingsSectionPreviewList } from "@/features/organization/components/settings-section-preview-list"
import { healthTone } from "@/features/organization/utils/webhook-health"
import { useWebhooksContentState } from "@/features/organization/hooks/use-webhooks"
import type { WebhookEndpoint } from "@/types/organization"

interface WebhooksSectionPreviewProps {
  organizationId: string
  canAccessWebhooks: boolean
  onViewAll: () => void
}

/**
 * Webhooks section content for Settings: first 5 endpoints as compact
 * cards (no inline delivery-expand/sparkline — that stays full-list-only),
 * resolving the same Available/Empty/Locked states the full panel does, via
 * the shared `useWebhooksContentState` hook so preview and overlay never
 * disagree.
 */
export function WebhooksSectionPreview({
  organizationId,
  canAccessWebhooks,
  onViewAll,
}: WebhooksSectionPreviewProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { state, endpoints } = useWebhooksContentState(
    organizationId,
    canAccessWebhooks
  )

  if (state === "loading") {
    return (
      <div className="flex flex-col gap-3">
        {[1, 2].map((i) => (
          <Skeleton key={i} className="h-12 w-full" />
        ))}
      </div>
    )
  }

  if (state === "locked") {
    return (
      <FeatureGateCard
        reason="plan"
        title={t("organization.webhooks.upgradeTitle")}
        description={t("organization.webhooks.upgradeDescription")}
        cta={{
          label: t("organization.webhooks.upgradeCta"),
          onClick: () =>
            void navigate({
              to: "/organization/$organizationId/billing",
              params: { organizationId },
            }),
        }}
      />
    )
  }

  if (state === "empty") {
    return (
      <div className="rounded-xl border bg-card">
        <EmptyState
          icon={IconWebhook}
          title={t("organization.webhooks.noEndpoints")}
          action={{ label: t("organization.webhooks.add"), onClick: onViewAll }}
        />
      </div>
    )
  }

  return (
    <SettingsSectionPreviewList<WebhookEndpoint>
      items={endpoints}
      keyExtractor={(ep) => ep.id}
      onViewAll={onViewAll}
      moreLabel={(count) => t("organization.webhooks.moreCount", { count })}
      renderItem={(ep) => {
        const percent = ep.health?.success_percent_24h ?? 100
        return (
          <div className="group flex items-center gap-4 px-4 py-3">
            <div
              className={`flex size-8 shrink-0 items-center justify-center rounded-full ${healthTone(percent)}`}
            >
              <IconWebhook className="size-4" />
            </div>

            <div className="flex flex-1 flex-col overflow-hidden">
              <div className="flex items-center gap-2">
                <span className="truncate font-mono text-sm font-medium text-foreground">
                  {ep.url}
                </span>
              </div>

              <div className="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                <span className="font-medium">
                  {percent}% {t("organization.webhooks.deliveryRateLabel")}
                </span>
                <span className="size-1 rounded-full bg-border" />
                {ep.enabled ? (
                  <span className="rounded-md bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-bold tracking-wider text-emerald-600 uppercase dark:text-emerald-400">
                    {t("organization.webhooks.enabledLabel")}
                  </span>
                ) : (
                  <span className="rounded-md bg-muted px-1.5 py-0.5 text-[10px] font-bold tracking-wider text-muted-foreground uppercase">
                    {t("organization.webhooks.disabledLabel")}
                  </span>
                )}
                {ep.subscribed_events && ep.subscribed_events.length > 0 && (
                  <>
                    <span className="size-1 rounded-full bg-border" />
                    <span>
                      {t("organization.webhooks.subscribedCount", {
                        count: ep.subscribed_events.length,
                      })}
                    </span>
                  </>
                )}
              </div>
            </div>

            <div className="hidden shrink-0 flex-col items-end gap-1.5 sm:flex">
              <span className="text-[10px] font-semibold tracking-wider text-muted-foreground uppercase">
                {t("organization.webhooks.last24h")}
              </span>
              <div className="flex h-1.5 w-24 overflow-hidden rounded-full bg-muted">
                {(ep.health?.total_24h ?? 0) > 0 ? (
                  <>
                    <div
                      className="bg-emerald-500"
                      style={{ width: `${percent}%` }}
                    />
                    <div
                      className="bg-destructive"
                      style={{ width: `${100 - percent}%` }}
                    />
                  </>
                ) : (
                  <div className="w-full bg-muted-foreground/20" />
                )}
              </div>
              <div className="flex gap-2 text-[10px] font-medium text-muted-foreground">
                <span className="text-emerald-600 dark:text-emerald-400">
                  {ep.health?.delivered_24h ?? 0}{" "}
                  {t("organization.webhooks.okLabel")}
                </span>
                <span className="text-destructive">
                  {(ep.health?.total_24h ?? 0) -
                    (ep.health?.delivered_24h ?? 0)}{" "}
                  {t("organization.webhooks.errLabel")}
                </span>
              </div>
            </div>
          </div>
        )
      }}
    />
  )
}
