import { useTranslation } from "react-i18next"
import { Link } from "@tanstack/react-router"
import { IconLoader2 } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
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
  activating: boolean
  onActivateNow: () => void
  onUpgrade: () => void
}

// Every subscription state gets one banner + one action.
export function SubscriptionStatusBanner({
  sub,
  organizationId,
  isOwner,
  daysLeft,
  resuming,
  onResume,
  activating,
  onActivateNow,
  onUpgrade,
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
            to="/organization/$organizationId/billing/invoices"
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
        <div className="flex gap-2">
          {isOwner && (
            <AlertDialog>
              <AlertDialogTrigger
                render={<Button size="sm" variant="outline" />}
              >
                {t("billing.trial.activateNow")}
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>
                    {t("billing.trial.activateNowTitle")}
                  </AlertDialogTitle>
                  <AlertDialogDescription>
                    {t("billing.trial.activateNowDescription")}
                  </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
                  <AlertDialogAction
                    disabled={activating}
                    onClick={onActivateNow}
                  >
                    {activating && (
                      <IconLoader2
                        data-icon="inline-start"
                        className="animate-spin"
                      />
                    )}
                    {activating
                      ? t("billing.trial.activating")
                      : t("billing.trial.activateNow")}
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          )}
          <Button size="sm" onClick={onUpgrade}>
            {t("billing.trial.upgradeCta")}
          </Button>
        </div>
      </div>
    )
  }

  return null
}
