import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import {
  useBillingSubscription,
  useResumeSubscription,
  useExtendSubscription,
  useActivateTrialNow,
} from "@/features/billing/hooks"
import { usePermissions } from "@/hooks/use-permissions"
import { PlanSelector } from "./plan-selector"
import { SubscriptionStatusBanner } from "./subscription-status-banner"
import { SubscriptionExtendDialog } from "./subscription-extend-dialog"
import { SubscriptionCancelDialog } from "./subscription-cancel-dialog"
import { SubscriptionDetailsGrid } from "./subscription-details-grid"

function trialDaysLeft(trialEnd?: string) {
  if (!trialEnd) return null
  const days = Math.ceil((new Date(trialEnd).getTime() - Date.now()) / 86400000)
  return days > 0 ? days : null
}

interface Props {
  organizationId: string
}

export function SubscriptionCard({ organizationId }: Props) {
  const { t } = useTranslation()
  const [showSelector, setShowSelector] = useState(false)
  const [extendOpen, setExtendOpen] = useState(false)
  const [extendMonths, setExtendMonths] = useState("1")

  const { data: subData, isLoading } = useBillingSubscription(organizationId)
  const { isOwner } = usePermissions()

  const sub = subData?.data

  const { mutate: resume, isPending: resuming } =
    useResumeSubscription(organizationId)
  const { mutate: extend, isPending: extending } =
    useExtendSubscription(organizationId)
  const { mutate: activateNow, isPending: activating } =
    useActivateTrialNow(organizationId)

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
    isOwner && (sub.status === "active" || sub.status === "trialing")
  // cancelled/expired both get a dedicated banner+action above (one
  // banner, one action per state) instead of a header button.
  // Extension only applies to an already-active, invoiced subscription —
  // trialing/cancelled/expired don't have a real billing period to extend.
  const canExtend = isOwner && sub.status === "active"

  return (
    <div className="flex flex-col gap-4">
      <SubscriptionStatusBanner
        sub={sub}
        organizationId={organizationId}
        isOwner={!!isOwner}
        daysLeft={daysLeft}
        resuming={resuming}
        onResume={() => resume()}
        activating={activating}
        onActivateNow={() => activateNow()}
        onUpgrade={() => setShowSelector(true)}
      />

      <Card>
        <CardHeader>
          <div className="flex flex-wrap items-start justify-between gap-2">
            <CardTitle>{t("billing.subscription.title")}</CardTitle>
            <div className="flex flex-wrap items-center gap-2">
              {isOwner ? (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setShowSelector(true)}
                >
                  {t("billing.subscription.changePlan")}
                </Button>
              ) : (
                <span className="text-xs text-muted-foreground">
                  {t("billing.managedByNote")}
                </span>
              )}
              {canExtend && (
                <SubscriptionExtendDialog
                  open={extendOpen}
                  onOpenChange={setExtendOpen}
                  months={extendMonths}
                  onMonthsChange={setExtendMonths}
                  extending={extending}
                  onExtend={() =>
                    extend(
                      { months: Number(extendMonths) },
                      { onSuccess: () => setExtendOpen(false) }
                    )
                  }
                />
              )}
              <SubscriptionCancelDialog
                organizationId={organizationId}
                periodEnd={sub.period_end}
                canCancel={canCancel}
              />
            </div>
          </div>
        </CardHeader>
        <CardContent>
          <SubscriptionDetailsGrid sub={sub} daysLeft={daysLeft} />
        </CardContent>
      </Card>

      <PlanSelector
        organizationId={organizationId}
        currentPlan={sub.plan}
        currentCycle={sub.cycle}
        currentPeriodEnd={sub.period_end}
        currency={sub.currency}
        open={showSelector}
        onOpenChange={setShowSelector}
      />
    </div>
  )
}
