import { Suspense, lazy, useState } from "react"
import { useTranslation } from "react-i18next"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import {
  useBillingSubscription,
  useResumeSubscription,
  useUndoScheduledCancellation,
} from "@/features/billing/hooks"
import { usePermissions } from "@/hooks/use-permissions"
import { PlanSelector } from "./plan-selector"
import { SubscriptionStatusBanner } from "./subscription-status-banner"
import { SubscriptionDetailsGrid } from "./subscription-details-grid"
import { SubscriptionHistorySection } from "./subscription-history-section"
import { FeaturesSection } from "./features-section"
import { OverageWarningCard } from "./overage-warning-card"

// Both wizards only surface once the owner opts in (Extend is gated behind
// `canExtend`, Cancel behind its own dialog trigger) — kept out of the
// subscription card's own chunk so a plain read-only visit doesn't load them.
const SubscriptionExtendDialog = lazy(() =>
  import("./subscription-extend-dialog").then((m) => ({
    default: m.SubscriptionExtendDialog,
  }))
)
const SubscriptionCancelDialog = lazy(() =>
  import("./subscription-cancel-dialog").then((m) => ({
    default: m.SubscriptionCancelDialog,
  }))
)

function trialDaysLeft(trialEnd?: string) {
  if (!trialEnd) return null
  const days = Math.ceil((new Date(trialEnd).getTime() - Date.now()) / 86400000)
  return days > 0 ? days : null
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
  organizationId: string
}

export function SubscriptionCard({ organizationId }: Props) {
  const { t } = useTranslation()
  const [showSelector, setShowSelector] = useState(false)

  const { data: subData, isLoading } = useBillingSubscription(organizationId)
  const { isOwner, hasPendingInvoice } = usePermissions()

  const sub = subData?.data

  const { mutate: resume, isPending: resuming } =
    useResumeSubscription(organizationId)
  const { mutate: undoCancellation, isPending: undoingCancellation } =
    useUndoScheduledCancellation(organizationId)

  if (isLoading) {
    return (
      <Card>
        <CardHeader>
          <Skeleton className="h-5 w-32" />
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {[48, 40, 36].map((w) => (
            <Skeleton key={w} className="h-4" style={{ width: w * 4 }} />
          ))}
        </CardContent>
      </Card>
    )
  }

  if (!sub) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>{t("billing.subscription.title")}</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-1">
          <p className="text-sm font-medium">
            {t("billing.subscription.noSubscription")}
          </p>
          <p className="text-sm text-muted-foreground">
            {t("billing.subscription.setupBilling")}
          </p>
        </CardContent>
      </Card>
    )
  }

  const daysLeft = trialDaysLeft(sub.trial_end)
  const canCancel =
    isOwner &&
    (sub.status === "active" ||
      sub.status === "trialing" ||
      sub.status === "past_due")
  // cancelled/expired both get a dedicated banner+action above (one
  // banner, one action per state) instead of a header button.
  // Extension only applies to an already-active, invoiced subscription —
  // trialing/cancelled/expired don't have a real billing period to extend.
  // Also blocked while any invoice on the subscription is still unpaid —
  // requesting an extension before the current one clears would leave two
  // invoices pending on the same subscription at once.
  const canExtend = isOwner && sub.status === "active" && !hasPendingInvoice
  // Unlike Extend, plan changes don't need an active billing period —
  // trialing has no invoice yet, expired/past_due may want to queue up a
  // different plan before/while reactivating. Only cancelled is blocked:
  // there's no billing to change until they resume first (banner action).
  const canChangePlan = isOwner && sub.status !== "cancelled"

  return (
    <div className="flex flex-col gap-4">
      <SubscriptionStatusBanner
        sub={sub}
        organizationId={organizationId}
        isOwner={!!isOwner}
        daysLeft={daysLeft}
        resuming={resuming}
        onResume={() => resume()}
        undoingCancellation={undoingCancellation}
        onUndoCancellation={() => undoCancellation()}
      />

      <Card className="[--card-spacing:--spacing(8)]">
        <CardHeader>
          <div className="flex flex-wrap items-start justify-between gap-2">
            <CardTitle className="text-2xl font-bold">
              {t("billing.subscription.title")}
            </CardTitle>
            <div className="flex flex-wrap items-center gap-2">
              {canChangePlan && (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setShowSelector(true)}
                >
                  {t("billing.subscription.changePlan")}
                </Button>
              )}
              {!isOwner && (
                <span className="text-xs text-muted-foreground">
                  {t("billing.managedByNote")}
                </span>
              )}
              {canExtend && (
                <Suspense fallback={null}>
                  <SubscriptionExtendDialog
                    organizationId={organizationId}
                    subscription={sub}
                  />
                </Suspense>
              )}
              <Suspense fallback={null}>
                <SubscriptionCancelDialog
                  organizationId={organizationId}
                  plan={sub.plan}
                  cycle={sub.cycle}
                  currency={sub.currency}
                  periodEnd={sub.period_end}
                  status={sub.status}
                  canCancel={canCancel}
                />
              </Suspense>
            </div>
          </div>
        </CardHeader>
        <CardContent className="flex flex-col gap-6">
          <OverageWarningCard organizationId={organizationId} />
          <SubscriptionDetailsGrid
            sub={sub}
            daysLeft={daysLeft}
            organizationId={organizationId}
            isOwner={!!isOwner}
          >
            <FeaturesSection organizationId={organizationId} />
          </SubscriptionDetailsGrid>
          <Separator />
          <div className="flex flex-col gap-8 md:flex-row">
            <SubscriptionHistorySection
              organizationId={organizationId}
              isOwner={!!isOwner}
            />
            <div className="flex-1 space-y-2">
              <p className="text-sm font-medium text-muted-foreground">
                {t("billing.subscription.latestActivity")}
              </p>
              <p className="text-sm text-muted-foreground">
                {t("billing.subscription.activeSince", {
                  date: formatDate(sub.period_start),
                })}
              </p>
            </div>
          </div>
        </CardContent>
      </Card>

      <PlanSelector
        organizationId={organizationId}
        currentPlan={sub.plan}
        currentCycle={sub.cycle}
        currentPeriodEnd={sub.period_end}
        currency={sub.currency}
        subscriptionStatus={sub.status}
        open={showSelector}
        onOpenChange={setShowSelector}
      />
    </div>
  )
}
