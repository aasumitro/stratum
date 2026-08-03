import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { IconArrowBackUp } from "@tabler/icons-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { useUndoScheduledDowngrade } from "@/features/billing/hooks"
import { cn } from "@/lib/ui"
import type { Subscription } from "@/types/billing"

const STATUS_BADGE: Record<string, string> = {
  active: "bg-emerald-500/10 text-emerald-600",
  trialing: "bg-sky-500/10 text-sky-600",
  cancelled: "bg-amber-500/10 text-amber-600",
  past_due: "bg-red-500/10 text-red-600",
  expired: "bg-destructive/10 text-destructive",
}

function formatDate(s?: string) {
  if (!s) return "—"
  return new Date(s).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })
}

interface Props {
  sub: Subscription
  daysLeft: number | null
  organizationId: string
  isOwner: boolean
  children?: ReactNode
}

export function SubscriptionDetailsGrid({
  sub,
  daysLeft,
  organizationId,
  isOwner,
  children,
}: Props) {
  const { t } = useTranslation()
  const { mutate: undoDowngrade, isPending: undoingDowngrade } =
    useUndoScheduledDowngrade(organizationId)
  return (
    // 2 columns below lg (not 1) — a single narrow column stacked all 3-4
    // groups full-height with a large gap between each, wasting the mobile
    // viewport's unused horizontal half and making a handful of short facts
    // (plan, status, cycle, two dates) scroll like a much longer page.
    <div className="grid grid-cols-2 gap-4 lg:grid-cols-4 lg:gap-6">
      <div className="lg:border-r lg:pr-6">
        <div className="space-y-4">
          <div className="space-y-1">
            <p className="text-sm font-medium text-muted-foreground">
              {t("billing.subscription.plan")}
            </p>
            <p className="text-2xl font-bold capitalize">{sub.plan}</p>
            {sub.scheduled_plan && (
              <div className="flex items-center gap-1.5">
                <Badge variant="outline">
                  {t("billing.downgrade.scheduledBadge", {
                    plan: sub.scheduled_plan,
                    date: formatDate(sub.period_end),
                  })}
                </Badge>
                {isOwner && (
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-6"
                    title={t("common.undo")}
                    aria-label={t("common.undo")}
                    disabled={undoingDowngrade}
                    onClick={() => undoDowngrade()}
                  >
                    <IconArrowBackUp className="size-3.5" />
                  </Button>
                )}
              </div>
            )}
          </div>
          {daysLeft !== null && (
            <div>
              <p className="text-sm font-medium text-muted-foreground">
                {t("billing.subscription.trialEnds")}
              </p>
              <p className="text-base font-medium text-sky-600">
                {t("billing.subscription.trialDaysLeft", { days: daysLeft })}
              </p>
            </div>
          )}
        </div>
      </div>

      <div className="lg:border-r lg:pr-6">
        <div className="space-y-4">
          <div>
            <p className="text-sm font-medium text-muted-foreground">
              {t("billing.subscription.status")}
            </p>
            <span
              className={cn(
                "inline-block rounded px-3 py-1 text-sm font-medium capitalize",
                STATUS_BADGE[sub.status] ?? ""
              )}
            >
              {sub.status.replace("_", " ")}
            </span>
          </div>
          <div>
            <p className="text-sm font-medium text-muted-foreground">
              {t("billing.subscription.cycle")}
            </p>
            <p className="text-base font-medium capitalize">{sub.cycle}</p>
          </div>
        </div>
      </div>

      <div className="lg:border-r lg:pr-6">
        <div className="space-y-4">
          <div>
            <p className="text-sm font-medium text-muted-foreground">
              {t("billing.subscription.periodStart")}
            </p>
            <p className="text-sm font-medium">
              {formatDate(sub.period_start)}
            </p>
          </div>
          <div>
            <p className="text-sm font-medium text-muted-foreground">
              {t("billing.subscription.periodEnd")}
            </p>
            <p className="text-sm font-medium">{formatDate(sub.period_end)}</p>
          </div>
        </div>
      </div>

      {children}
    </div>
  )
}
