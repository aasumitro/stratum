import { useTranslation } from "react-i18next"
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
  undoingCancellation: boolean
  onUndoCancellation: () => void
}

// Every subscription state gets one banner + one action.
export function SubscriptionStatusBanner({
  sub,
  organizationId,
  isOwner,
  daysLeft,
  undoingCancellation,
  onUndoCancellation,
}: Props) {
  const { t } = useTranslation()

  // Scheduled cancellation: status stays active/trialing/past_due until the
  // cancellation takes effect at the end of the period (or trial), so this
  // is a sub-state status alone doesn't capture. Once it applies, status is
  // "cancelled" and the dunning banner (billing-layout.tsx) takes over.
  if (sub.scheduled_cancel_at && sub.status !== "cancelled") {
    return (
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-amber-500/30 bg-amber-500/10 px-4 py-3">
        <div>
          <p className="text-sm font-semibold text-amber-700 dark:text-amber-400">
            {t("billing.states.scheduledCancelTitle")}
          </p>
          <p className="mt-0.5 text-xs text-amber-600/80 dark:text-amber-500/80">
            {t("billing.states.scheduledCancelDescription", {
              date: formatDate(sub.period_end),
            })}
          </p>
        </div>
        {isOwner && (
          <Button
            size="sm"
            variant="outline"
            disabled={undoingCancellation}
            onClick={onUndoCancellation}
          >
            {undoingCancellation && (
              <IconLoader2 data-icon="inline-start" className="animate-spin" />
            )}
            {t("billing.states.scheduledCancelUndo")}
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

  // past_due, expired and cancelled render nothing here on purpose: the
  // dunning banner at the top of the page (billing-layout.tsx) shows each of
  // them once, with the action that resolves it — same state, one banner.
  return null
}
