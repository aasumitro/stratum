import { Suspense, lazy, useState } from "react"
import { useTranslation } from "react-i18next"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { usePlans, useInvoicePreview } from "@/features/billing/hooks"
import { useOrganization } from "@/features/organization/hooks/use-organization"
import { usePermissions } from "@/hooks/use-permissions"
import { formatPrice } from "@/features/billing/utils"
import type { BillingCycle, SubscriptionStatus } from "@/types/billing"
import { cn } from "@/lib/ui"
import { InvoicePreviewNote } from "./invoice-preview-note"

// Only entered once the user picks a downgrade/upgrade target and hits
// Continue — kept out of this dialog's chunk so opening the plan picker
// itself doesn't pull either wizard in.
const DowngradeWizard = lazy(() =>
  import("./downgrade-wizard").then((m) => ({ default: m.DowngradeWizard }))
)
const UpgradeWizard = lazy(() =>
  import("./upgrade-wizard").then((m) => ({ default: m.UpgradeWizard }))
)

interface Props {
  organizationId: string
  currentPlan: string
  currentCycle: BillingCycle
  currentPeriodEnd?: string
  currency: string
  subscriptionStatus: SubscriptionStatus
  open: boolean
  onOpenChange: (open: boolean) => void
}

// Change plan is PATCH plan; proration converts remaining value to
// TIME (period end moves, no charge/refund today), never the other way
// around. The dialog states that explicitly so users don't expect a
// refund/charge, backed by a real preview (GET .../billing/preview) instead
// of recomputing the proration math client-side — this is the same
// prorate() the backend actually charges with, so it can't drift.
export function PlanSelector({
  organizationId,
  currentPlan,
  currentCycle,
  currentPeriodEnd,
  currency,
  subscriptionStatus,
  open,
  onOpenChange,
}: Props) {
  const { t } = useTranslation()
  const [cycle, setCycle] = useState<BillingCycle>(currentCycle)
  const [selectedPlan, setSelectedPlan] = useState(currentPlan)

  const { hasPendingInvoice } = usePermissions()
  // country_code, not the subscription's own currency field, is what scopes
  // this fetch down to one currency — a customer must never receive (or be
  // able to pick from) pricing for a currency they can't actually be billed
  // in. Kept disabled until it's known, rather than firing once unscoped
  // and again once scoped.
  const { data: orgData, isLoading: orgLoading } =
    useOrganization(organizationId)
  const countryCode = orgData?.data?.country_code ?? ""
  const { data, isLoading: plansLoading } = usePlans(countryCode, !orgLoading)
  const isLoading = orgLoading || plansLoading

  const plans = (data?.data ?? [])
    .filter((p) => p.active)
    .sort((a, b) => a.sort_order - b.sort_order)
  const selected = plans.find((p) => p.id === selectedPlan)
  const current = plans.find((p) => p.id === currentPlan)
  // Every plan above was scoped by the same country_code, so each one's
  // price map holds exactly one currency — read it back from the data
  // itself for display, rather than the `currency` prop, so this never
  // depends on the two staying in sync.
  const displayCurrency = Object.keys(plans[0]?.prices ?? {})[0] ?? currency

  const isChanging = selectedPlan !== currentPlan || cycle !== currentCycle
  const isDowngrade =
    selected && current && selected.sort_order < current.sort_order

  const [showDowngradeWizard, setShowDowngradeWizard] = useState(false)
  const [showUpgradeWizard, setShowUpgradeWizard] = useState(false)
  const { data: previewData, isFetching: previewLoading } = useInvoicePreview(
    organizationId,
    selectedPlan,
    cycle,
    open && isChanging && selectedPlan !== "custom"
  )
  const preview = previewData?.data

  function reset() {
    setSelectedPlan(currentPlan)
    setCycle(currentCycle)
    setShowDowngradeWizard(false)
    setShowUpgradeWizard(false)
  }

  // Every close path (X/escape, footer Cancel, a wizard's own Done/close)
  // must go through this so PlanSelector's own state — which wizard step
  // to show, the in-progress plan/cycle selection — never survives into
  // the next time the modal is opened.
  function handleOpenChange(v: boolean) {
    if (!v) reset()
    onOpenChange(v)
  }

  // Deliberately not re-checking isDowngrade/isChanging here: they're only
  // meant to gate *entry* into a wizard (see where setShowDowngradeWizard/
  // setShowUpgradeWizard(true) are called below). Once a wizard is open, a
  // successful change updates currentPlan/currentCycle via query
  // invalidation, which flips these on the very next render — re-checking
  // them here would unmount the wizard out from under itself before its own
  // success step ever gets a chance to render.
  if (showDowngradeWizard) {
    return (
      <Suspense fallback={null}>
        <DowngradeWizard
          organizationId={organizationId}
          open={open}
          onOpenChange={handleOpenChange}
          targetPlan={selectedPlan}
          targetCycle={cycle}
          currentPlan={currentPlan}
          currentCycle={currentCycle}
          currentPeriodEnd={currentPeriodEnd}
          currency={currency}
          subscriptionStatus={subscriptionStatus}
          onBackToPlans={() => setShowDowngradeWizard(false)}
        />
      </Suspense>
    )
  }

  if (showUpgradeWizard) {
    return (
      <Suspense fallback={null}>
        <UpgradeWizard
          organizationId={organizationId}
          open={open}
          onOpenChange={handleOpenChange}
          targetPlan={selectedPlan}
          targetCycle={cycle}
          currentPlan={currentPlan}
          currentCycle={currentCycle}
          currentPeriodEnd={currentPeriodEnd}
          currency={currency}
          onBackToPlans={() => setShowUpgradeWizard(false)}
        />
      </Suspense>
    )
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("billing.plans.changePlanTitle")}</DialogTitle>
          <DialogDescription>
            {t("billing.plans.changePlanDescription")}
          </DialogDescription>
        </DialogHeader>

        <div className="flex items-center justify-end">
          <div className="flex rounded-lg border p-0.5 text-xs">
            {(["monthly", "yearly"] as BillingCycle[]).map((c) => (
              <button
                key={c}
                onClick={() => setCycle(c)}
                className={cn(
                  "rounded-md px-3 py-1 font-medium capitalize transition-colors",
                  cycle === c
                    ? "bg-primary text-primary-foreground"
                    : "text-muted-foreground hover:text-foreground"
                )}
              >
                {t(`billing.plans.${c}`)}
              </button>
            ))}
          </div>
        </div>

        {isLoading ? (
          <div className="flex flex-col gap-2">
            {[1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        ) : (
          <div className="flex flex-col gap-1.5">
            {plans.map((plan) => {
              const isCustom = plan.id === "custom"
              const isCurrent = plan.id === currentPlan
              const prices = plan.prices[displayCurrency]
              const amount = prices
                ? cycle === "monthly"
                  ? prices.monthly
                  : prices.yearly
                : 0
              return (
                <label
                  key={plan.id}
                  className={cn(
                    "flex cursor-pointer items-center gap-3 rounded-lg border p-3 text-sm transition-colors",
                    selectedPlan === plan.id
                      ? "border-primary bg-primary/5"
                      : "hover:bg-accent/50",
                    isCustom && "cursor-default"
                  )}
                >
                  <input
                    type="radio"
                    name="plan"
                    className="accent-primary"
                    checked={selectedPlan === plan.id}
                    disabled={isCustom}
                    onChange={() => setSelectedPlan(plan.id)}
                  />
                  <div className="flex flex-1 items-center justify-between gap-2">
                    <span className="font-medium">{plan.name}</span>
                    {isCustom ? (
                      <span className="text-muted-foreground">
                        {t("billing.plans.contactUs")}
                      </span>
                    ) : (
                      <span className="text-muted-foreground">
                        {formatPrice(amount, displayCurrency, cycle, t)}
                      </span>
                    )}
                  </div>
                  {isCurrent && (
                    <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
                      {t("billing.plans.currentPlan")}
                    </span>
                  )}
                </label>
              )
            })}
          </div>
        )}

        {isChanging && selectedPlan !== "custom" && (
          <InvoicePreviewNote
            preview={preview}
            loading={previewLoading}
            currentPeriodEnd={currentPeriodEnd}
            planName={selected?.name ?? selectedPlan}
            hasPendingInvoice={hasPendingInvoice}
          />
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => handleOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={!isChanging || selectedPlan === "custom" || !selected}
            onClick={() => {
              if (isDowngrade) {
                setShowDowngradeWizard(true)
              } else {
                setShowUpgradeWizard(true)
              }
            }}
          >
            {t("common.continue")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
