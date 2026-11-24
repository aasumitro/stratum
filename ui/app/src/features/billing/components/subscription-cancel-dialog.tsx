import { lazy, Suspense, useState } from "react"
import { useTranslation } from "react-i18next"
import { Link } from "@tanstack/react-router"
import { IconLoader2 } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import {
  useOrgPlansCatalog,
  useCancelSubscription,
  useUndoScheduledCancellation,
} from "@/features/billing/hooks"
import { usePermissions } from "@/hooks/use-permissions"
import { formatPrice } from "@/features/billing/utils"
import type {
  BillingCycle,
  CancelReason,
  SubscriptionStatus,
} from "@/types/billing"
import {
  retentionOffers,
  type RetentionOffer,
} from "@/features/billing/retention-offer"

// The offer's own wizards, loaded only when an offer is taken.
const DowngradeWizard = lazy(() =>
  import("./downgrade-wizard").then((m) => ({ default: m.DowngradeWizard }))
)
const UpgradeWizard = lazy(() =>
  import("./upgrade-wizard").then((m) => ({ default: m.UpgradeWizard }))
)

// Shown for these reasons when the build sets VITE_CONTACT_URL.
const CONTACT_REASONS: CancelReason[] = [
  "missing_features",
  "switching_provider",
]

function formatDate(s?: string) {
  if (!s) return "—"
  return new Date(s).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })
}

const REASONS: CancelReason[] = [
  "too_expensive",
  "missing_features",
  "switching_provider",
  "no_longer_needed",
  "other",
]

interface Props {
  organizationId: string
  plan: string
  cycle: BillingCycle
  currency: string
  periodEnd?: string
  canCancel: boolean
  subscriptionStatus: SubscriptionStatus
  // False while a trial runs (nothing is paid to save on) or any change is
  // already scheduled (a plan change, a cancellation, an add-on decrease):
  // an offer would replace it or be refused.
  offersAllowed: boolean
}

type Step = "reason" | "review" | "success"

export function SubscriptionCancelDialog({
  organizationId,
  plan,
  cycle,
  currency,
  periodEnd,
  canCancel,
  subscriptionStatus,
  offersAllowed,
}: Props) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [step, setStep] = useState<Step>("reason")
  const [reason, setReason] = useState<CancelReason | "">("")
  const [details, setDetails] = useState("")
  const [takenOffer, setTakenOffer] = useState<RetentionOffer | null>(null)

  // A cancel never ends access early, on any status (trialing included): it
  // is scheduled for the end of the paid period or trial, so the success
  // step always offers to undo the schedule.
  const { mutate: cancel, isPending: cancelling } =
    useCancelSubscription(organizationId)
  const { mutate: undoCancellation, isPending: undoingCancellation } =
    useUndoScheduledCancellation(organizationId)
  const { data: plansData } = useOrgPlansCatalog(organizationId)
  // Only an unpaid activation stays owed after cancelling — it backs the
  // period in use. A pending renewal invoice is voided by the cancel.
  const { hasBlockingInvoice } = usePermissions()

  const planInfo = (plansData?.data ?? []).find((p) => p.id === plan)
  // The API always scopes prices down to one, server-resolved currency —
  // read it back from the data itself, rather than the `currency` prop, so
  // this never depends on the two staying in sync.
  const displayCurrency = Object.keys(planInfo?.prices ?? {})[0] ?? currency
  const prices = planInfo?.prices[displayCurrency]
  const priceLabel = prices
    ? formatPrice(
        cycle === "monthly" ? prices.monthly : prices.yearly,
        displayCurrency,
        cycle,
        t
      )
    : undefined

  const contactURL = import.meta.env.VITE_CONTACT_URL as string | undefined
  // A web page opens in a new tab; a mailto: link must not leave an empty one.
  const contactNewTab = /^https?:/i.test(contactURL ?? "")

  const offers = offersAllowed
    ? retentionOffers(plansData?.data ?? [], plan, cycle, displayCurrency)
    : []

  // Taking an offer swaps this dialog for that change's own wizard.
  if (takenOffer) {
    const wizardProps = {
      organizationId,
      open: true,
      // Closing the wizard ends the whole flow; Back returns to the reasons.
      onOpenChange: (v: boolean) => {
        if (v) return
        setTakenOffer(null)
        setReason("")
        setDetails("")
      },
      targetPlan: takenOffer.plan.id,
      targetCycle: takenOffer.cycle,
      currentPlan: plan,
      currentCycle: cycle,
      currentPeriodEnd: periodEnd,
      currency,
      onBackToPlans: () => {
        setTakenOffer(null)
        setOpen(true)
      },
    }
    return (
      <Suspense fallback={null}>
        {takenOffer.kind === "downgrade" ? (
          <DowngradeWizard
            {...wizardProps}
            subscriptionStatus={subscriptionStatus}
          />
        ) : (
          <UpgradeWizard {...wizardProps} />
        )}
      </Suspense>
    )
  }

  function takeOffer(offer: RetentionOffer) {
    setOpen(false)
    setStep("reason")
    setTakenOffer(offer)
  }

  // The subscription refetches after a cancel, and canCancel can turn false
  // underneath an open dialog (the period ending meanwhile). Unmounting then
  // would tear the dialog down mid-flow before its success step renders, so
  // once open it stays mounted through its own close.
  if (!canCancel && !open) return null

  function handleClose() {
    setOpen(false)
    // reset after animation
    setTimeout(() => {
      setStep("reason")
      setReason("")
      setDetails("")
    }, 300)
  }

  function handleConfirm() {
    if (!reason) return
    cancel(
      { reason, details: details || undefined },
      { onSuccess: () => setStep("success") }
    )
  }

  if (step === "success") {
    return (
      <Dialog open={open} onOpenChange={handleClose}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              {t("billing.cancel.scheduledSuccessTitle")}
            </DialogTitle>
            <DialogDescription>
              {t("billing.cancel.scheduledSuccessDescription", {
                date: formatDate(periodEnd),
              })}
            </DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-2 rounded-lg bg-muted p-3 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {t("billing.cancel.planLabel")}
              </span>
              <span className="font-medium">
                {planInfo?.name ?? plan}
                {priceLabel ? ` — ${priceLabel}` : ""}
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {t("billing.cancel.accessUntilLabel")}
              </span>
              <span className="font-medium">{formatDate(periodEnd)}</span>
            </div>
          </div>

          <DialogFooter>
            <Button
              variant="outline"
              disabled={undoingCancellation}
              onClick={() =>
                undoCancellation(undefined, { onSuccess: handleClose })
              }
            >
              {undoingCancellation && (
                <IconLoader2
                  data-icon="inline-start"
                  className="animate-spin"
                />
              )}
              {t("billing.states.scheduledCancelUndo")}
            </Button>
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
            <DialogTitle>{t("billing.cancel.reviewTitle")}</DialogTitle>
            <DialogDescription>
              {t("billing.cancel.reviewAccessUntil", {
                date: formatDate(periodEnd),
              })}
            </DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-2 rounded-lg bg-muted p-3 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {t("billing.cancel.planLabel")}
              </span>
              <span className="font-medium">
                {planInfo?.name ?? plan}
                {priceLabel ? ` — ${priceLabel}` : ""}
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {t("billing.cancel.accessUntilLabel")}
              </span>
              <span className="font-medium">{formatDate(periodEnd)}</span>
            </div>
          </div>

          {hasBlockingInvoice && (
            <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-700 dark:text-amber-400">
              {t("billing.cancel.pendingInvoiceNote")}
            </div>
          )}

          <div className="flex flex-col gap-3 py-2 text-sm text-muted-foreground">
            <p>{t("billing.cancel.reviewAfterPeriodEnd")}</p>
            <div className="rounded-md border p-3">
              <p className="mb-2 font-medium text-foreground">
                {t("billing.cancel.exportTitle")}
              </p>
              <ul className="list-inside list-disc">
                <li>
                  <Link to="/account" className="underline">
                    {t("billing.cancel.exportPersonalData")}
                  </Link>
                </li>
                <li>
                  <Link
                    to="/organization/$organizationId/settings"
                    params={{ organizationId }}
                    search={{ panel: "audit-log" }}
                    className="underline"
                  >
                    {t("billing.cancel.exportOrganizationData")}
                  </Link>
                </li>
              </ul>
            </div>
          </div>

          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setStep("reason")}
              disabled={cancelling}
            >
              {t("common.back")}
            </Button>
            <Button
              variant="destructive"
              onClick={handleConfirm}
              disabled={cancelling}
            >
              {cancelling && (
                <IconLoader2
                  data-icon="inline-start"
                  className="animate-spin"
                />
              )}
              {t("billing.cancel.confirmCancel")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    )
  }

  return (
    <Dialog open={open} onOpenChange={(v) => (v ? setOpen(v) : handleClose())}>
      <DialogTrigger render={<Button variant="outline" size="sm" />}>
        {t("billing.subscription.cancel")}
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("billing.cancel.reasonTitle")}</DialogTitle>
          <DialogDescription>
            {t("billing.cancel.reasonDescription")}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4 py-2">
          <RadioGroup
            value={reason}
            onValueChange={(v) => setReason((v as CancelReason) ?? "")}
          >
            {REASONS.map((r) => (
              <div key={r} className="flex flex-col gap-1">
                <label className="flex items-center gap-2">
                  <RadioGroupItem value={r} />
                  <span className="text-sm font-medium">
                    {t(`billing.cancel.reasons.${r}`)}
                  </span>
                </label>
                {reason === r && r !== "other" && (
                  <p className="ml-6 text-xs text-muted-foreground">
                    {t(`billing.cancel.hints.${r}`)}
                  </p>
                )}
                {reason === r && r === "too_expensive" && offers.length > 0 && (
                  <div className="ml-6 flex flex-col gap-2">
                    {offers.map((o) => (
                      <div
                        key={o.kind}
                        className="flex items-center justify-between gap-3 rounded-lg border p-2.5"
                      >
                        <p className="text-xs">
                          {t(`billing.cancel.offers.${o.kind}`, {
                            plan: o.plan.name,
                            price: formatPrice(
                              o.price,
                              displayCurrency,
                              o.cycle,
                              t
                            ),
                            instead: formatPrice(
                              o.instead,
                              displayCurrency,
                              o.cycle,
                              t
                            ),
                            saving: formatPrice(
                              o.saving,
                              displayCurrency,
                              o.cycle,
                              t
                            ),
                          })}
                        </p>
                        <Button
                          type="button"
                          size="sm"
                          variant="outline"
                          className="shrink-0"
                          onClick={() => takeOffer(o)}
                        >
                          {t(`billing.cancel.offers.${o.kind}Action`)}
                        </Button>
                      </div>
                    ))}
                  </div>
                )}
                {reason === r && CONTACT_REASONS.includes(r) && contactURL && (
                  <a
                    href={contactURL}
                    target={contactNewTab ? "_blank" : undefined}
                    rel="noopener noreferrer"
                    className="ml-6 text-xs font-medium underline underline-offset-4"
                  >
                    {t("billing.cancel.offers.contact")}
                  </a>
                )}
              </div>
            ))}
          </RadioGroup>

          <div className="flex flex-col gap-1.5">
            <label className="text-sm font-medium">
              {t("billing.cancel.detailsLabel")}{" "}
              <span className="font-normal text-muted-foreground">
                ({t("common.optional")})
              </span>
            </label>
            <Textarea
              value={details}
              onChange={(e) => setDetails(e.target.value)}
              placeholder={t("billing.cancel.detailsPlaceholder")}
              maxLength={500}
            />
          </div>
        </div>

        <DialogFooter>
          <Button onClick={() => setStep("review")} disabled={!reason}>
            {t("common.continue")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
