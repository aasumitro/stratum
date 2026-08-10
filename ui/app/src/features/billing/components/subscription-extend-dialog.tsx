import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconLoader2, IconMinus, IconPlus } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { Skeleton } from "@/components/ui/skeleton"
import {
  useOrgPlansCatalog,
  useExtendSubscription,
  useAttachedAddons,
} from "@/features/billing/hooks"
import {
  computeExtensionAddonSubtotal,
  computeExtensionSubtotal,
} from "@/features/billing/proration"
import { formatMoney } from "@/lib/format"
import type { Invoice, Subscription } from "@/types/billing"

function formatDate(d: Date) {
  return d.toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })
}

function addMonths(from: string | undefined, months: number): Date | null {
  if (!from) return null
  const d = new Date(from)
  d.setMonth(d.getMonth() + months)
  return d
}

interface Props {
  organizationId: string
  subscription: Subscription
}

type Step = "choose" | "review" | "success"
type Mode = "months" | "annual"

export function SubscriptionExtendDialog({
  organizationId,
  subscription,
}: Props) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [step, setStep] = useState<Step>("choose")
  const [mode, setMode] = useState<Mode>("months")
  const [months, setMonths] = useState(1)
  const [result, setResult] = useState<Invoice | null>(null)

  const { data: plansData, isLoading: plansLoading } =
    useOrgPlansCatalog(organizationId)
  const { data: addonsData } = useAttachedAddons(organizationId)
  const { mutate: extend, isPending: extending } =
    useExtendSubscription(organizationId)

  const maxExtendable = subscription.max_extendable_months
  const planInfo = (plansData?.data ?? []).find(
    (p) => p.id === subscription.plan
  )
  // The API always scopes prices down to one, server-resolved currency —
  // read it back from the data itself, rather than subscription.currency,
  // so this never depends on the two staying in sync.
  const prices = planInfo?.prices[Object.keys(planInfo?.prices ?? {})[0] ?? ""]

  // Only currently-attached addons are charged on extend — same "live
  // quantity only, ignore scheduled/pending" rule the backend applies
  // (extendSubscription / computeExtensionAddonSubtotal).
  const attachedAddons = (addonsData?.data ?? []).filter((a) => a.quantity > 0)

  // Every option this component offers is only reachable through here — no
  // separate cap enforcement to keep in sync with the backend's own.
  const monthsCap = Math.max(1, Math.min(24, maxExtendable))
  const canSwitchToAnnual = subscription.cycle === "monthly"
  const switchToAnnualEligible = canSwitchToAnnual && maxExtendable >= 12

  const blocks = Math.floor(months / 12)
  const remainder = months % 12
  const planMonthsSubtotal = computeExtensionSubtotal(prices, months)
  const addonMonthsSubtotal = computeExtensionAddonSubtotal(
    attachedAddons,
    subscription.currency,
    months
  )
  const monthsSubtotal = planMonthsSubtotal + addonMonthsSubtotal
  const flatMonthlyTotal =
    (prices?.monthly ?? 0) * months +
    attachedAddons.reduce(
      (sum, a) =>
        sum + (a.prices[subscription.currency]?.monthly ?? 0) * a.quantity,
      0
    ) *
      months
  const monthsSavings = flatMonthlyTotal - monthsSubtotal
  const monthsSavingsPercent =
    flatMonthlyTotal > 0
      ? Math.round((monthsSavings / flatMonthlyTotal) * 100)
      : 0

  const planAnnualSubtotal = prices?.yearly ?? 0
  const addonAnnualSubtotal = computeExtensionAddonSubtotal(
    attachedAddons,
    subscription.currency,
    12
  )
  const annualSubtotal = planAnnualSubtotal + addonAnnualSubtotal
  const annualFlatTotal =
    (prices?.monthly ?? 0) * 12 +
    attachedAddons.reduce(
      (sum, a) =>
        sum + (a.prices[subscription.currency]?.monthly ?? 0) * a.quantity,
      0
    ) *
      12
  const annualSavings = annualFlatTotal - annualSubtotal
  const annualSavingsPercent =
    annualFlatTotal > 0
      ? Math.round((annualSavings / annualFlatTotal) * 100)
      : 0

  const currentPeriodEnd = subscription.period_end
    ? new Date(subscription.period_end)
    : null
  const newPeriodEndMonths = addMonths(subscription.period_end, months)
  const newPeriodEndAnnual = addMonths(subscription.period_end, 12)
  const newPeriodEnd =
    mode === "annual" ? newPeriodEndAnnual : newPeriodEndMonths

  function handleClose(v: boolean) {
    if (!v) {
      setOpen(false)
      setTimeout(() => {
        setStep("choose")
        setMode("months")
        setMonths(1)
        setResult(null)
      }, 300)
      return
    }
    setOpen(v)
  }

  function handleConfirm() {
    extend(mode === "annual" ? { switch_to_annual: true } : { months }, {
      onSuccess: (data) => {
        setResult(data.data ?? null)
        setStep("success")
      },
    })
  }

  const trigger = (
    <Button
      variant="outline"
      size="sm"
      disabled={maxExtendable === 0}
      onClick={() => setOpen(true)}
    >
      {t("billing.extend.trigger")}
    </Button>
  )

  const successNewPeriodEnd = result?.switch_to_annual
    ? newPeriodEndAnnual
    : newPeriodEndMonths

  return (
    <>
      {maxExtendable === 0 ? (
        <Tooltip>
          <TooltipTrigger render={<span>{trigger}</span>} />
          <TooltipContent>{t("billing.extend.capReachedNote")}</TooltipContent>
        </Tooltip>
      ) : (
        trigger
      )}

      {step === "success" && (
        <Dialog open={open} onOpenChange={handleClose}>
          <DialogContent className="sm:max-w-md">
            <DialogHeader>
              <DialogTitle>{t("billing.extend.successTitle")}</DialogTitle>
              <DialogDescription>
                {result
                  ? t("billing.extend.successDescription")
                  : t("billing.extend.successDescriptionFallback")}
              </DialogDescription>
            </DialogHeader>

            {result && (
              <div className="flex flex-col gap-2 rounded-lg bg-muted p-3 text-sm">
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground">
                    {t("billing.extend.amountDueLabel")}
                  </span>
                  <span className="font-medium">
                    {formatMoney(result.amount_cents, result.currency)}
                  </span>
                </div>
                {result.due_at && (
                  <div className="flex items-center justify-between">
                    <span className="text-muted-foreground">
                      {t("billing.extend.dueDateLabel")}
                    </span>
                    <span className="font-medium">
                      {formatDate(new Date(result.due_at))}
                    </span>
                  </div>
                )}
                {successNewPeriodEnd && (
                  <div className="flex items-center justify-between">
                    <span className="text-muted-foreground">
                      {t("billing.extend.newRenewalLabel")}
                    </span>
                    <span className="font-medium">
                      {formatDate(successNewPeriodEnd)}
                    </span>
                  </div>
                )}
                <p className="pt-1 text-xs text-muted-foreground">
                  {result.switch_to_annual
                    ? t("billing.extend.pendingPaymentAnnualNote")
                    : t("billing.extend.pendingPaymentNote")}
                </p>
              </div>
            )}

            <DialogFooter>
              <Button onClick={() => handleClose(false)}>
                {t("common.done")}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}

      {step === "review" && (
        <Dialog open={open} onOpenChange={handleClose}>
          <DialogContent className="sm:max-w-md">
            <DialogHeader>
              <DialogTitle>{t("billing.extend.reviewTitle")}</DialogTitle>
              <DialogDescription>
                {t("billing.extend.reviewDescription")}
              </DialogDescription>
            </DialogHeader>

            <div className="flex flex-col gap-2 rounded-lg bg-muted p-3 text-sm">
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">
                  {t("billing.extend.optionLabel")}
                </span>
                <span className="font-medium">
                  {mode === "annual"
                    ? t("billing.extend.optionSwitchAnnual")
                    : t("billing.extend.optionExtend", { count: months })}
                </span>
              </div>
              {currentPeriodEnd && (
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground">
                    {t("billing.extend.currentRenewalLabel")}
                  </span>
                  <span>{formatDate(currentPeriodEnd)}</span>
                </div>
              )}
              {newPeriodEnd && (
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground">
                    {t("billing.extend.newRenewalLabel")}
                  </span>
                  <span className="font-medium text-primary">
                    {formatDate(newPeriodEnd)}
                  </span>
                </div>
              )}
            </div>

            <div className="flex flex-col gap-1.5 rounded-lg border p-3 text-sm">
              {plansLoading ? (
                <Skeleton className="h-6 w-full" />
              ) : mode === "annual" ? (
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground">
                    {t("billing.extend.lineTwelveMonthsYearly")}
                  </span>
                  <span>
                    {formatMoney(planAnnualSubtotal, subscription.currency)}
                  </span>
                </div>
              ) : (
                <>
                  {blocks >= 1 && (
                    <div className="flex items-center justify-between">
                      <span className="text-muted-foreground">
                        {t("billing.extend.lineYearlyBlocks", { blocks })}
                      </span>
                      <span>
                        {formatMoney(
                          blocks * (prices?.yearly ?? 0),
                          subscription.currency
                        )}
                      </span>
                    </div>
                  )}
                  {(blocks === 0 || remainder > 0) && (
                    <div className="flex items-center justify-between">
                      <span className="text-muted-foreground">
                        {t("billing.extend.lineMonthlyRemainder", {
                          count: blocks === 0 ? months : remainder,
                        })}
                      </span>
                      <span>
                        {formatMoney(
                          (blocks === 0 ? months : remainder) *
                            (prices?.monthly ?? 0),
                          subscription.currency
                        )}
                      </span>
                    </div>
                  )}
                </>
              )}

              {!plansLoading &&
                attachedAddons.map((addon) => {
                  const addonPrices = addon.prices[subscription.currency]
                  if (!addonPrices) return null
                  return (
                    <div key={addon.addon_id} className="contents">
                      {mode === "annual" ? (
                        <div className="flex items-center justify-between">
                          <span className="text-muted-foreground">
                            {t("billing.extend.addonLineTwelveMonthsYearly", {
                              name: addon.name,
                              quantity: addon.quantity,
                            })}
                          </span>
                          <span>
                            {formatMoney(
                              addonPrices.yearly * addon.quantity,
                              subscription.currency
                            )}
                          </span>
                        </div>
                      ) : (
                        <>
                          {blocks >= 1 && (
                            <div className="flex items-center justify-between">
                              <span className="text-muted-foreground">
                                {t("billing.extend.addonLineYearlyBlocks", {
                                  name: addon.name,
                                  quantity: addon.quantity,
                                  blocks,
                                })}
                              </span>
                              <span>
                                {formatMoney(
                                  blocks * addonPrices.yearly * addon.quantity,
                                  subscription.currency
                                )}
                              </span>
                            </div>
                          )}
                          {(blocks === 0 || remainder > 0) && (
                            <div className="flex items-center justify-between">
                              <span className="text-muted-foreground">
                                {t("billing.extend.addonLineMonthlyRemainder", {
                                  name: addon.name,
                                  quantity: addon.quantity,
                                  count: blocks === 0 ? months : remainder,
                                })}
                              </span>
                              <span>
                                {formatMoney(
                                  (blocks === 0 ? months : remainder) *
                                    addonPrices.monthly *
                                    addon.quantity,
                                  subscription.currency
                                )}
                              </span>
                            </div>
                          )}
                        </>
                      )}
                    </div>
                  )
                })}

              {!plansLoading && (
                <>
                  <div className="flex items-center justify-between border-t pt-1.5 font-medium">
                    <span>{t("billing.extend.subtotalLabel")}</span>
                    <span>
                      {formatMoney(
                        mode === "annual" ? annualSubtotal : monthsSubtotal,
                        subscription.currency
                      )}
                    </span>
                  </div>
                  <div className="flex items-center justify-between text-muted-foreground">
                    <span>{t("billing.extend.taxLabel")}</span>
                    <span>{t("billing.extend.taxAtCheckout")}</span>
                  </div>
                  <p className="pt-1 text-xs text-muted-foreground">
                    {t("billing.extend.finalAmountNote")}
                  </p>
                </>
              )}
            </div>

            <p className="text-sm text-muted-foreground">
              {mode === "annual"
                ? t("billing.extend.consequenceAnnual")
                : newPeriodEndMonths
                  ? t("billing.extend.consequenceMonths", {
                      date: formatDate(newPeriodEndMonths),
                    })
                  : ""}
            </p>

            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => setStep("choose")}
                disabled={extending}
              >
                {t("common.back")}
              </Button>
              <Button onClick={handleConfirm} disabled={extending}>
                {extending && (
                  <IconLoader2
                    data-icon="inline-start"
                    className="animate-spin"
                  />
                )}
                {t("billing.extend.confirmExtend")}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}

      {step === "choose" && (
        <Dialog open={open} onOpenChange={handleClose}>
          <DialogContent className="sm:max-w-md">
            <DialogHeader>
              <DialogTitle>{t("billing.extend.chooseTitle")}</DialogTitle>
              <DialogDescription>
                {t("billing.extend.chooseDescription")}
              </DialogDescription>
            </DialogHeader>

            {currentPeriodEnd && (
              <div className="rounded-lg bg-muted px-3 py-2 text-sm">
                <span className="text-muted-foreground">
                  {t("billing.extend.currentRenewalLabel")}
                </span>{" "}
                <span className="font-medium">
                  {formatDate(currentPeriodEnd)}
                </span>
              </div>
            )}

            <div className="flex flex-col gap-4 py-2">
              <div className="flex flex-col gap-2">
                <label className="text-sm font-medium">
                  {t("billing.extend.monthsLabel")}
                </label>
                <div className="flex items-center gap-1.5">
                  <Button
                    type="button"
                    variant="outline"
                    size="icon"
                    className="size-8"
                    aria-label={t("billing.extend.decreaseMonths")}
                    disabled={months <= 1}
                    onClick={() => setMonths((m) => Math.max(1, m - 1))}
                  >
                    <IconMinus className="size-3.5" />
                  </Button>
                  <span className="flex size-8 items-center justify-center rounded-md border text-sm">
                    {months}
                  </span>
                  <Button
                    type="button"
                    variant="outline"
                    size="icon"
                    className="size-8"
                    aria-label={t("billing.extend.increaseMonths")}
                    disabled={months >= monthsCap}
                    onClick={() => setMonths((m) => Math.min(monthsCap, m + 1))}
                  >
                    <IconPlus className="size-3.5" />
                  </Button>
                </div>

                {!plansLoading && newPeriodEndMonths && (
                  <p className="text-xs text-muted-foreground">
                    {t("billing.extend.renewsOn", {
                      date: formatDate(newPeriodEndMonths),
                    })}{" "}
                    ·{" "}
                    {t("billing.extend.subtotal", {
                      amount: formatMoney(
                        monthsSubtotal,
                        subscription.currency
                      ),
                    })}{" "}
                    {t("billing.extend.plusTax")}
                  </p>
                )}

                {blocks >= 1 && (
                  <p className="text-xs text-muted-foreground">
                    {remainder > 0
                      ? t("billing.extend.tieredBreakdownWithRemainder", {
                          blocks,
                          remainder,
                        })
                      : t("billing.extend.tieredBreakdownBlocksOnly", {
                          blocks,
                        })}
                  </p>
                )}

                {monthsSavings > 0 && (
                  <p className="text-xs font-medium text-emerald-600 dark:text-emerald-500">
                    {t("billing.extend.savingsNote", {
                      amount: formatMoney(monthsSavings, subscription.currency),
                      percent: monthsSavingsPercent,
                    })}
                  </p>
                )}

                {months === 12 && switchToAnnualEligible && (
                  <div className="flex flex-col gap-1.5 rounded-md border border-dashed p-2.5">
                    <p className="text-xs text-muted-foreground">
                      {t("billing.extend.exactlyTwelveNudge")}
                    </p>
                    <Button
                      type="button"
                      variant="link"
                      size="sm"
                      className="h-auto self-start p-0 text-xs"
                      onClick={() => {
                        setMode("annual")
                        setStep("review")
                      }}
                    >
                      {t("billing.extend.switchToAnnual")}
                    </Button>
                  </div>
                )}

                {blocks >= 1 && months !== 12 && switchToAnnualEligible && (
                  <p className="text-xs text-muted-foreground">
                    {t("billing.extend.cycleStaysMonthly")}
                  </p>
                )}

                {months >= monthsCap && (
                  <p className="text-xs text-muted-foreground">
                    {t("billing.extend.maxReachedWarning")}
                  </p>
                )}
              </div>

              {canSwitchToAnnual &&
                (switchToAnnualEligible ? (
                  <div className="flex flex-col gap-2 rounded-md border p-3">
                    <p className="text-sm font-medium">
                      {t("billing.extend.switchToAnnualTitle")}
                    </p>
                    <p className="text-sm text-muted-foreground">
                      {t("billing.extend.switchToAnnualDescription")}
                    </p>
                    {newPeriodEndAnnual && (
                      <p className="text-xs text-muted-foreground">
                        {t("billing.extend.renewsOn", {
                          date: formatDate(newPeriodEndAnnual),
                        })}{" "}
                        ·{" "}
                        {t("billing.extend.subtotal", {
                          amount: formatMoney(
                            annualSubtotal,
                            subscription.currency
                          ),
                        })}{" "}
                        {t("billing.extend.plusTax")}
                      </p>
                    )}
                    {annualSavings > 0 && (
                      <p className="text-xs font-medium text-emerald-600 dark:text-emerald-500">
                        {t("billing.extend.savingsNote", {
                          amount: formatMoney(
                            annualSavings,
                            subscription.currency
                          ),
                          percent: annualSavingsPercent,
                        })}
                      </p>
                    )}
                    <p className="text-xs text-muted-foreground">
                      {t("billing.extend.switchToAnnualCycleChangeNote")}
                    </p>
                    <Button
                      type="button"
                      variant="outline"
                      className="self-start"
                      onClick={() => {
                        setMode("annual")
                        setStep("review")
                      }}
                    >
                      {t("billing.extend.switchToAnnual")}
                    </Button>
                  </div>
                ) : (
                  <p className="text-xs text-muted-foreground">
                    {t("billing.extend.switchToAnnualUnavailable")}
                  </p>
                ))}
            </div>

            <DialogFooter>
              <Button
                onClick={() => {
                  setMode("months")
                  setStep("review")
                }}
              >
                {t("common.continue")}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </>
  )
}
