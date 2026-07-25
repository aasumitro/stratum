import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconLoader2, IconCheck } from "@tabler/icons-react"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import {
  usePlans,
  useFeatures,
  useChangePlan,
  useInvoicePreview,
} from "@/features/billing/hooks"
import { formatPrice } from "@/features/billing/utils"
import { formatMoney, formatBytes } from "@/lib/format"
import type { BillingCycle } from "@/types/billing"

function formatDate(s?: string) {
  if (!s) return "—"
  return new Date(s).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })
}

// Plan.limits is keyed by Feature.id (e.g. "storage"), which is distinct
// from Feature.metric_key (e.g. "storage_bytes") — the id identifies the
// catalog row, the metric_key identifies the unit/format. Byte-formatting
// must key off metric_key, not id.
function formatLimitValue(
  metricKey: string | undefined,
  value: number
): string {
  if (metricKey === "storage_bytes") return formatBytes(value)
  return value.toLocaleString()
}

interface Props {
  organizationId: string
  open: boolean
  onOpenChange: (open: boolean) => void
  targetPlan: string
  targetCycle: BillingCycle
  currentPlan: string
  currentCycle: BillingCycle
  currentPeriodEnd?: string
  currency: string
  onBackToPlans: () => void
}

type Step = "changes" | "review" | "success"

export function UpgradeWizard({
  organizationId,
  open,
  onOpenChange,
  targetPlan,
  targetCycle,
  currentPlan,
  currentCycle,
  currentPeriodEnd,
  currency,
  onBackToPlans,
}: Props) {
  const { t } = useTranslation()
  const [step, setStep] = useState<Step>("changes")
  const [termsAgreed, setTermsAgreed] = useState(false)

  const { data: plansData, isLoading: plansLoading } = usePlans()
  const { data: catalogData } = useFeatures()
  const { mutate: changePlan, isPending: changing } =
    useChangePlan(organizationId)
  const { data: previewData, isFetching: previewLoading } = useInvoicePreview(
    organizationId,
    targetPlan,
    targetCycle,
    open && (step === "changes" || step === "review")
  )
  const preview = previewData?.data

  const plans = plansData?.data ?? []
  const targetPlanInfo = plans.find((p) => p.id === targetPlan)
  const currentPlanInfo = plans.find((p) => p.id === currentPlan)
  // Falls back to USD same as PlanSelector's own picker list, in case the
  // org's currency isn't a key in this plan's price map.
  const targetPrices =
    targetPlanInfo?.prices[currency] ?? targetPlanInfo?.prices["USD"]
  const featureCatalog = new Map(
    (catalogData?.data ?? []).map((f) => [f.id, f])
  )

  const planChanged = targetPlan !== currentPlan
  const cycleChanged = targetCycle !== currentCycle
  const newFeatureIds = planChanged
    ? (targetPlanInfo?.features ?? []).filter(
        (f) => !(currentPlanInfo?.features ?? []).includes(f)
      )
    : []

  function handleClose() {
    onOpenChange(false)
    setTimeout(() => {
      setStep("changes")
      setTermsAgreed(false)
    }, 300)
  }

  function handleConfirm() {
    if (!termsAgreed) return
    changePlan(
      { plan: targetPlan, cycle: targetCycle, terms_agreed: true },
      { onSuccess: () => setStep("success") }
    )
  }

  if (step === "success") {
    return (
      <Dialog open={open} onOpenChange={handleClose}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("billing.upgrade.successTitle")}</DialogTitle>
            <DialogDescription>
              {t("billing.upgrade.successDescription", {
                plan: targetPlanInfo?.name ?? targetPlan,
              })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button onClick={handleClose}>{t("common.done")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    )
  }

  if (step === "review") {
    return (
      <Dialog open={open} onOpenChange={handleClose}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("billing.upgrade.reviewTitle")}</DialogTitle>
            <DialogDescription>
              {t("billing.upgrade.reviewDescription")}
            </DialogDescription>
          </DialogHeader>

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

          <div className="flex items-start gap-2 py-2">
            <Checkbox
              id="upgrade-terms-agreed"
              checked={termsAgreed}
              onCheckedChange={(v) => setTermsAgreed(v === true)}
            />
            <Label
              htmlFor="upgrade-terms-agreed"
              className="text-sm font-normal"
            >
              {t("billing.upgrade.termsAgreed")}
            </Label>
          </div>

          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setStep("changes")}
              disabled={changing}
            >
              {t("common.back")}
            </Button>
            <Button
              onClick={handleConfirm}
              disabled={changing || previewLoading || !termsAgreed}
            >
              {changing && (
                <IconLoader2
                  data-icon="inline-start"
                  className="animate-spin"
                />
              )}
              {t("billing.upgrade.confirmUpgrade")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    )
  }

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("billing.upgrade.changesTitle")}</DialogTitle>
          <DialogDescription>
            {t("billing.upgrade.changesDescription")}
          </DialogDescription>
        </DialogHeader>

        {plansLoading ? (
          <div className="py-4">
            <Skeleton className="h-20 w-full" />
          </div>
        ) : (
          <div className="flex flex-col gap-4 py-2 text-sm">
            {planChanged && (
              <div className="flex flex-col gap-2">
                <p className="font-medium">
                  {t("billing.upgrade.newFeatures", {
                    plan: targetPlanInfo?.name ?? targetPlan,
                  })}
                </p>
                {newFeatureIds.length > 0 ? (
                  <ul className="flex flex-col gap-1.5">
                    {newFeatureIds.map((f) => (
                      <li key={f} className="flex items-center gap-2">
                        <IconCheck className="size-4 shrink-0 text-emerald-500" />
                        <span>{featureCatalog.get(f)?.name ?? f}</span>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p className="text-muted-foreground">
                    {t("billing.upgrade.noNewFeatures")}
                  </p>
                )}
                {targetPlanInfo?.limits && (
                  <ul className="flex flex-col gap-1 text-muted-foreground">
                    {Object.entries(targetPlanInfo.limits).map(
                      ([featureId, value]) => (
                        <li key={featureId}>
                          {featureCatalog.get(featureId)?.name ?? featureId}:{" "}
                          {formatLimitValue(
                            featureCatalog.get(featureId)?.metric_key,
                            value
                          )}
                        </li>
                      )
                    )}
                  </ul>
                )}
              </div>
            )}

            {cycleChanged && (
              <div className="flex flex-col gap-2 rounded-md border p-3">
                <p className="font-medium">
                  {t("billing.upgrade.cycleChangeTitle")}
                </p>
                <p className="text-muted-foreground">
                  {t("billing.upgrade.cycleChangeFrom", {
                    from: t(`billing.plans.${currentCycle}`),
                    to: t(`billing.plans.${targetCycle}`),
                  })}
                </p>
                {targetPrices && (
                  <p className="text-muted-foreground">
                    {t("billing.upgrade.newPrice", {
                      price: formatPrice(
                        targetCycle === "monthly"
                          ? targetPrices.monthly
                          : targetPrices.yearly,
                        currency,
                        targetCycle,
                        t
                      ),
                    })}
                  </p>
                )}
                {!planChanged && (
                  <p className="text-muted-foreground">
                    {t("billing.upgrade.featuresUnchanged")}
                  </p>
                )}
              </div>
            )}
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={onBackToPlans}>
            {t("common.back")}
          </Button>
          <Button onClick={() => setStep("review")}>
            {t("common.continue")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
