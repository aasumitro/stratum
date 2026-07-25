import { useTranslation } from "react-i18next"
import { Link } from "@tanstack/react-router"
import { IconLoader2 } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { ActivateTrialDialog } from "./activate-trial-dialog"
import type { Subscription } from "@/types/billing"

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
  organizationId: string
  isOwner: boolean
  daysLeft: number | null
  resuming: boolean
  onResume: () => void
}

// Every subscription state gets one banner + one action.
export function SubscriptionStatusBanner({
  sub,
  organizationId,
  isOwner,
  daysLeft,
  resuming,
  onResume,
}: Props) {
  const { t } = useTranslation()

  if (sub.status === "past_due") {
    return (
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-red-500/30 bg-red-500/10 px-4 py-3">
        <div>
          <p className="text-sm font-semibold text-red-700 dark:text-red-400">
            {t("billing.states.pastDueTitle")}
          </p>
          <p className="mt-0.5 text-xs text-red-600/80 dark:text-red-500/80">
            {t("billing.states.pastDueDescription")}
          </p>
        </div>
        {isOwner && (
          <Link
            to="/organization/$organizationId/billing"
            params={{ organizationId }}
            className="inline-flex items-center rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground"
          >
            {t("billing.states.payInvoice")}
          </Link>
        )}
      </div>
    )
  }

  if (sub.status === "expired") {
    return (
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-destructive/30 bg-destructive/10 px-4 py-3">
        <div>
          <p className="text-sm font-semibold text-destructive">
            {t("billing.states.expiredTitle")}
          </p>
          <p className="mt-0.5 text-xs text-destructive/80">
            {t("billing.states.expiredDescription")}
          </p>
        </div>
        {isOwner && (
          <Button size="sm" disabled={resuming} onClick={onResume}>
            {resuming && (
              <IconLoader2 data-icon="inline-start" className="animate-spin" />
            )}
            {t("billing.states.resumeGetLink")}
          </Button>
        )}
      </div>
    )
  }

  if (sub.status === "cancelled") {
    return (
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border bg-muted px-4 py-3">
        <p className="text-sm">
          {t("billing.states.cancelledAccessUntil", {
            date: formatDate(sub.period_end),
          })}
        </p>
        {isOwner && (
          <Button
            size="sm"
            variant="outline"
            disabled={resuming}
            onClick={onResume}
          >
            {resuming && (
              <IconLoader2 data-icon="inline-start" className="animate-spin" />
            )}
            {t("billing.subscription.resume")}
          </Button>
        )}
      </div>
    )
  }

  if (sub.status === "trialing") {
    return (
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-sky-500/30 bg-sky-500/10 px-4 py-3">
        <div>
          <p className="text-sm font-semibold text-sky-700 dark:text-sky-400">
            {t("billing.trial.banner")}
            {daysLeft !== null && (
              <span className="ml-1 font-normal">
                — {t("billing.trial.daysLeft", { days: daysLeft })}
              </span>
            )}
          </p>
          <p className="mt-0.5 text-xs text-sky-600/80 dark:text-sky-500/80">
            {t("billing.trial.description")}
          </p>
        </div>
        {isOwner && (
          <div className="flex gap-2">
            {/* Choosing a different plan already lives in the Subscription
                card's own "Change plan" button just below — a second entry
                point here would just duplicate it. */}
            <ActivateTrialDialog organizationId={organizationId} />
          </div>
        )}
      </div>
    )
  }

  return null
}
