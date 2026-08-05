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
import { useOrganization } from "@/features/organization/hooks/use-organization"
import { usePermissions } from "@/hooks/use-permissions"
import { formatPrice } from "@/features/billing/utils"
import { formatMoney } from "@/lib/format"
import type { BillingCycle, InvoicePreview } from "@/types/billing"
import { InvoicePreviewNote } from "./invoice-preview-note"

function formatDate(s?: string) {
  if (!s) return "—"
  return new Date(s).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })
}

function formatLimitValue(value: number): string {
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
  // Frozen at confirm time, not read live from the query in the success
  // step — useChangePlan's onSuccess invalidates the subscription query,
  // and this component stays mounted through that, so the preview hook
  // could in principle refetch under different (post-change) inputs before
  // the success view renders. Capturing it once removes any dependency on
  // that race.
  const [confirmedPreview, setConfirmedPreview] =
    useState<InvoicePreview | null>(null)
  // Same freeze-at-confirm reasoning as confirmedPreview above, but for
  // whether a pending invoice existed going in — changePlanWithMetadata
  // (backend) voids it and issues a fresh one when it does, so the success
  // copy must reflect that instead of the plain proration/charge text.
  const [hadPendingInvoiceAtConfirm, setHadPendingInvoiceAtConfirm] =
    useState(false)

  const { hasPendingInvoice } = usePermissions()
  // country_code, not the subscription's own currency field, is what scopes
  // this fetch down to one currency — kept disabled until it's known,
  // rather than firing once unscoped and again once scoped.
  const { data: orgData, isLoading: orgLoading } =
    useOrganization(organizationId)
  const countryCode = orgData?.data?.country_code ?? ""
  const { data: plansData, isLoading: plansDataLoading } = usePlans(
    countryCode,
    !orgLoading
  )
  const plansLoading = orgLoading || plansDataLoading
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
  // Every plan above was scoped by the same country_code, so its price map
  // holds exactly one currency — read it back from the data itself, rather
  // than the `currency` prop, so this never depends on the two staying in
  // sync.
  const displayCurrency =
    Object.keys(targetPlanInfo?.prices ?? {})[0] ?? currency
  const targetPrices = targetPlanInfo?.prices[displayCurrency]
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
      setConfirmedPreview(null)
      setHadPendingInvoiceAtConfirm(false)
    }, 300)
  }

  function handleConfirm() {
    if (!termsAgreed) return
    changePlan(
      { plan: targetPlan, cycle: targetCycle, terms_agreed: true },
      {
        onSuccess: () => {
          setConfirmedPreview(preview ?? null)
          setHadPendingInvoiceAtConfirm(hasPendingInvoice)
          setStep("success")
        },
      }
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

          <div className="flex flex-col gap-2 rounded-lg bg-muted p-3 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {t("billing.upgrade.planLabel")}
              </span>
              <span className="font-medium">
                {targetPlanInfo?.name ?? targetPlan}
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {t("billing.upgrade.cycleLabel")}
              </span>
              <span className="font-medium">
                {t(`billing.plans.${targetCycle}`)}
              </span>
            </div>
            {hadPendingInvoiceAtConfirm && confirmedPreview ? (
              <p className="pt-1 text-xs text-muted-foreground">
                {t("billing.upgrade.successNewInvoiceIssued", {
                  amount: formatMoney(
                    confirmedPreview.total_cents,
                    confirmedPreview.currency
                  ),
                })}
              </p>
            ) : confirmedPreview?.new_period_end ? (
              <p className="pt-1 text-xs text-muted-foreground">
                {t("billing.plans.noChargeToday")}{" "}
                {t("billing.upgrade.successRenewsOn", {
                  date: formatDate(confirmedPreview.new_period_end),
                })}
              </p>
            ) : confirmedPreview ? (
              <p className="pt-1 text-xs text-muted-foreground">
                {t("billing.upgrade.successCharged", {
                  amount: formatMoney(
                    confirmedPreview.total_cents,
                    confirmedPreview.currency
                  ),
                })}
              </p>
            ) : null}
          </div>

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

          <div className="flex flex-col gap-2 rounded-lg bg-muted p-3 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {t("billing.upgrade.currentPlanLabel")}
              </span>
              <span className="font-medium">
                {currentPlanInfo?.name ?? currentPlan}
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {t("billing.upgrade.selectedPlanLabel")}
              </span>
              <span className="font-medium">
                {targetPlanInfo?.name ?? targetPlan}
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {t("billing.upgrade.cycleLabel")}
              </span>
              <span className="font-medium">
                {cycleChanged
                  ? `${t(`billing.plans.${currentCycle}`)} → ${t(`billing.plans.${targetCycle}`)}`
                  : t(`billing.plans.${targetCycle}`)}
              </span>
            </div>
          </div>

          <InvoicePreviewNote
            preview={preview}
            loading={previewLoading}
            currentPeriodEnd={currentPeriodEnd}
            planName={targetPlanInfo?.name ?? targetPlan}
            hasPendingInvoice={hasPendingInvoice}
          />

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
                          {formatLimitValue(value)}
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
                        displayCurrency,
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
