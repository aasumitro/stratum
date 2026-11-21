import { useState } from "react"
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
import type { BillingCycle, CancelReason } from "@/types/billing"

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
}

type Step = "reason" | "review" | "success"

export function SubscriptionCancelDialog({
  organizationId,
  plan,
  cycle,
  currency,
  periodEnd,
  canCancel,
}: Props) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [step, setStep] = useState<Step>("reason")
  const [reason, setReason] = useState<CancelReason | "">("")
  const [details, setDetails] = useState("")

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
              <label key={r} className="flex flex-col gap-1">
                <div className="flex items-center gap-2">
                  <RadioGroupItem value={r} />
                  <span className="text-sm font-medium">
                    {t(`billing.cancel.reasons.${r}`)}
                  </span>
                </div>
                {reason === r && r !== "other" && (
                  <p className="ml-6 text-xs text-muted-foreground">
                    {t(`billing.cancel.hints.${r}`)}
                  </p>
                )}
              </label>
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
