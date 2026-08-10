import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconRobot, IconUser, IconWebhook } from "@tabler/icons-react"
import { SideDrawer } from "@/components/shared/side-drawer"
import { StatusBadge } from "@/components/shared/status-badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import {
  useBillingHistory,
  useOrgAddonsCatalog,
} from "@/features/billing/hooks"
import { parseAddonChangeMetadata } from "@/features/billing/utils"
import { capitalize, formatMoney } from "@/lib/format"
import type { SubscriptionHistory } from "@/types/billing"

const PAGE_SIZE = 8

function formatDate(s: string) {
  return new Date(s).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })
}

// One consistent badge for every actor kind — a real user gets a generic
// person icon rather than initials, matching the system/webhook badges.
function ActorBadge({
  name,
  kind,
}: {
  name: string
  kind: "user" | "system" | "webhook"
}) {
  const Icon =
    kind === "webhook" ? IconWebhook : kind === "system" ? IconRobot : IconUser
  return (
    <span className="inline-flex w-fit items-center gap-1.5 rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
      <Icon className="size-3" />
      {name}
    </span>
  )
}

function logLine(
  h: SubscriptionHistory,
  addonNameById: Map<string, string>,
  t: (key: string, opts?: Record<string, unknown>) => string
) {
  switch (h.action) {
    case "upgrade":
    case "downgrade": {
      // A same-plan cycle-only switch (e.g. yearly -> monthly) reads as a
      // no-op "X -> X" unless called out on its own line; a plan change
      // that also changes cycle gets both in one line instead of two.
      const cycleChanged =
        h.from_cycle && h.to_cycle && h.from_cycle !== h.to_cycle
      const planChanged = h.from_plan !== h.to_plan
      if (!planChanged && cycleChanged) {
        return t("billing.history.logCycleChange", {
          fromCycle: capitalize(h.from_cycle!),
          toCycle: capitalize(h.to_cycle!),
        })
      }
      if (planChanged && cycleChanged) {
        return t("billing.history.logChangeWithCycle", {
          fromPlan: h.from_plan ? capitalize(h.from_plan) : "—",
          toPlan: h.to_plan ? capitalize(h.to_plan) : "—",
          fromCycle: capitalize(h.from_cycle!),
          toCycle: capitalize(h.to_cycle!),
        })
      }
      return t("billing.history.logChange", {
        fromPlan: h.from_plan ? capitalize(h.from_plan) : "—",
        toPlan: h.to_plan ? capitalize(h.to_plan) : "—",
      })
    }
    case "trial":
    case "activate":
      return t("billing.history.logStart", {
        toPlan: h.to_plan ? capitalize(h.to_plan) : "—",
      })
    case "cancel":
      return t("billing.history.logCancel")
    case "resume":
      return t("billing.history.logResume")
    case "expire":
      return t("billing.history.logExpire")
    case "extend":
      return t("billing.history.logExtend")
    case "addon_change": {
      const meta = parseAddonChangeMetadata(h.metadata)
      if (!meta) return null
      return t("billing.history.logAddonChange", {
        addonName: addonNameById.get(meta.addon_id) ?? meta.addon_id,
        fromQuantity: meta.from_quantity,
        toQuantity: meta.to_quantity,
      })
    }
  }
}

interface Props {
  organizationId: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function SubscriptionHistorySheet({
  organizationId,
  open,
  onOpenChange,
}: Props) {
  const { t } = useTranslation()
  const { data, isLoading } = useBillingHistory(organizationId)
  const { data: addonsData } = useOrgAddonsCatalog(organizationId)
  const [visibleCount, setVisibleCount] = useState(PAGE_SIZE)
  const history = data?.data ?? []
  const visible = history.slice(0, visibleCount)
  const addonNameById = new Map(
    (addonsData?.data ?? []).map((a) => [a.id, a.name])
  )

  return (
    <SideDrawer
      open={open}
      onOpenChange={onOpenChange}
      title={t("billing.history.title")}
      description={t("billing.history.description")}
      className="sm:max-w-md"
    >
      {isLoading ? (
        <div className="flex flex-col gap-3 py-4">
          {[1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-16 w-full" />
          ))}
        </div>
      ) : !history.length ? (
        <p className="py-4 text-sm text-muted-foreground">
          {t("billing.history.noHistory")}
        </p>
      ) : (
        <div className="flex flex-col gap-3 py-4">
          <ul className="flex flex-col gap-2">
            {visible.map((h) => (
              <li
                key={h.id}
                className="flex flex-col gap-1.5 rounded-lg border p-3"
              >
                <div className="flex items-center justify-between gap-2">
                  <ActorBadge
                    name={h.changed_by_name}
                    kind={h.changed_by_kind}
                  />
                  {h.phase && (
                    <StatusBadge
                      status={h.phase}
                      label={t(`billing.history.phase.${h.phase}`)}
                    />
                  )}
                </div>
                <p className="text-sm font-medium">
                  {logLine(h, addonNameById, t)}
                </p>
                <p className="text-xs text-muted-foreground">
                  {h.amount_cents > 0
                    ? `${formatMoney(h.amount_cents, h.currency)} · `
                    : ""}
                  {formatDate(h.changed_at)}
                </p>
                {h.effective_at && h.phase && h.phase !== "undone" && (
                  <p className="text-xs text-muted-foreground">
                    {t(
                      h.phase === "applied"
                        ? "billing.history.effectiveApplied"
                        : "billing.history.effectiveScheduled",
                      { date: formatDate(h.effective_at) }
                    )}
                  </p>
                )}
              </li>
            ))}
          </ul>
          {visibleCount < history.length && (
            <Button
              variant="outline"
              className="w-full"
              onClick={() => setVisibleCount((n) => n + PAGE_SIZE)}
            >
              {t("billing.history.loadMore")}
            </Button>
          )}
        </div>
      )}
    </SideDrawer>
  )
}
