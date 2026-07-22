import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconLoader2 } from "@tabler/icons-react"
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
import {
  usePlans,
  useChangePlan,
  useInvoicePreview,
} from "@/features/billing/hooks"
import { formatPrice } from "@/features/billing/utils"
import { formatMoney } from "@/lib/format"
import type { BillingCycle } from "@/types/billing"
import { cn } from "@/lib/ui"

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
  currentPlan: string
  currentCycle: BillingCycle
  currentPeriodEnd?: string
  currency: string
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
  open,
  onOpenChange,
}: Props) {
  const { t } = useTranslation()
  const [cycle, setCycle] = useState<BillingCycle>(currentCycle)
  const [selectedPlan, setSelectedPlan] = useState(currentPlan)

  const { data, isLoading } = usePlans()
  const { mutate: changePlan, isPending: changing } =
    useChangePlan(organizationId)

  const isChanging = selectedPlan !== currentPlan || cycle !== currentCycle
  const { data: previewData, isFetching: previewLoading } = useInvoicePreview(
    organizationId,
    selectedPlan,
    cycle,
    open && isChanging && selectedPlan !== "custom"
  )
  const preview = previewData?.data

  const plans = (data?.data ?? [])
    .filter((p) => p.active)
    .sort((a, b) => a.sort_order - b.sort_order)
  const selected = plans.find((p) => p.id === selectedPlan)

  function reset() {
    setSelectedPlan(currentPlan)
    setCycle(currentCycle)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) reset()
        onOpenChange(v)
      }}
    >
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
              const prices = plan.prices[currency] ?? plan.prices["USD"]
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
                        {formatPrice(amount, currency, cycle, t)}
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
          <div className="rounded-lg bg-muted p-3 text-sm">
            {previewLoading || !preview ? (
              <Skeleton className="h-8 w-full" />
            ) : preview.new_period_end ? (
              <p>
                <b>{t("billing.plans.noChargeToday")}</b>{" "}
                {t("billing.plans.prorationExplainer", {
                  newDate: formatDate(preview.new_period_end),
                  oldDate: formatDate(currentPeriodEnd),
                })}
              </p>
            ) : (
              <p>
                {t("billing.plans.nextInvoiceTotal", {
                  amount: formatMoney(preview.total_cents, preview.currency),
                })}
              </p>
            )}
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={
              !isChanging || selectedPlan === "custom" || changing || !selected
            }
            onClick={() =>
              changePlan(
                { plan: selectedPlan, cycle },
                { onSuccess: () => onOpenChange(false) }
              )
            }
          >
            {changing && (
              <IconLoader2 data-icon="inline-start" className="animate-spin" />
            )}
            {changing
              ? t("billing.plans.selecting")
              : t("billing.plans.confirmChange")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
