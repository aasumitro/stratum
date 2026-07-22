import { useTranslation } from "react-i18next"
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
}

export function SubscriptionDetailsGrid({ sub, daysLeft }: Props) {
  const { t } = useTranslation()
  return (
    <dl className="grid grid-cols-2 gap-x-6 gap-y-3 text-sm sm:grid-cols-3">
      <div>
        <dt className="text-muted-foreground">
          {t("billing.subscription.plan")}
        </dt>
        <dd className="mt-0.5 font-medium capitalize">{sub.plan}</dd>
      </div>
      <div>
        <dt className="text-muted-foreground">
          {t("billing.subscription.status")}
        </dt>
        <dd className="mt-0.5">
          <span
            className={cn(
              "rounded-full px-2 py-0.5 text-xs font-medium capitalize",
              STATUS_BADGE[sub.status] ?? ""
            )}
          >
            {sub.status.replace("_", " ")}
          </span>
        </dd>
      </div>
      <div>
        <dt className="text-muted-foreground">
          {t("billing.subscription.cycle")}
        </dt>
        <dd className="mt-0.5 font-medium capitalize">{sub.cycle}</dd>
      </div>
      <div>
        <dt className="text-muted-foreground">
          {t("billing.subscription.periodStart")}
        </dt>
        <dd className="mt-0.5 font-medium">{formatDate(sub.period_start)}</dd>
      </div>
      <div>
        <dt className="text-muted-foreground">
          {t("billing.subscription.periodEnd")}
        </dt>
        <dd className="mt-0.5 font-medium">{formatDate(sub.period_end)}</dd>
      </div>
      {daysLeft !== null && (
        <div>
          <dt className="text-muted-foreground">
            {t("billing.subscription.trialEnds")}
          </dt>
          <dd className="mt-0.5 font-medium text-sky-600">
            {t("billing.subscription.trialDaysLeft", { days: daysLeft })}
          </dd>
        </div>
      )}
    </dl>
  )
}
