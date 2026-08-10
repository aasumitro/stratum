import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconArrowBackUp } from "@tabler/icons-react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Badge } from "@/components/ui/badge"
import { ConfirmationDialog } from "@/components/shared/confirmation-dialog"
import { AddAddonsDialog } from "@/features/organization/components/add-addons-dialog"
import { ReviewChangesDialog } from "@/features/billing/components/review-changes-dialog"
import { OverageWarningCard } from "@/features/billing/components/overage-warning-card"
import { PayButton } from "@/features/billing/components/pay-button"
import {
  useOrgAddonsCatalog,
  useAttachedAddons,
  useAttachAddon,
  useDetachAddon,
  useUndoScheduledAddonChange,
  useBillingSubscription,
  useInvoices,
} from "@/features/billing/hooks"
import { usePermissions } from "@/hooks/use-permissions"
import {
  formatPrice,
  computeAmendmentDiff,
  applyAmendmentDiff,
} from "@/features/billing/utils"
import type { AmendmentDiff, AmendmentChange } from "@/features/billing/utils"

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

export function AddonsSection({ organizationId }: Props) {
  const { t } = useTranslation()
  const { isOwner } = usePermissions()
  const [pickerRequested, setPickerRequested] = useState(false)
  const [reviewStaged, setReviewStaged] = useState<Record<
    string,
    number
  > | null>(null)
  const [trialConfirmDiff, setTrialConfirmDiff] =
    useState<AmendmentDiff | null>(null)
  const [trialConfirmSubmitting, setTrialConfirmSubmitting] = useState(false)
  const [trialConfirmFailed, setTrialConfirmFailed] = useState<
    AmendmentChange[]
  >([])

  const { data: catalogData, isLoading: catalogLoading } =
    useOrgAddonsCatalog(organizationId)
  const { data: attachedData, isLoading: attachedLoading } =
    useAttachedAddons(organizationId)
  const { data: subData } = useBillingSubscription(organizationId)
  // Backs the pending-payment Pay button below — an addon row only carries
  // pending_invoice_id, not the invoice's own amount/currency PayButton needs.
  const { data: invoicesData } = useInvoices(organizationId)
  const invoicesById = new Map(
    (invoicesData?.data ?? []).map((inv) => [inv.id, inv])
  )
  const { mutateAsync: attach, isPending: attaching } =
    useAttachAddon(organizationId)
  const { mutateAsync: detach, isPending: detaching } =
    useDetachAddon(organizationId)
  const { mutate: undoScheduledChange, isPending: undoingSchedule } =
    useUndoScheduledAddonChange(organizationId)

  const catalog = (catalogData?.data ?? []).filter((a) => a.active)
  const catalogById = new Map(catalog.map((a) => [a.id, a]))
  const attached = attachedData?.data ?? []
  const cycle = subData?.data?.cycle ?? "monthly"
  const isTrialing = subData?.data?.status === "trialing"
  const periodEnd = subData?.data?.period_end
  // The API always scopes prices down to one, server-resolved currency —
  // read it back from the data itself rather than the subscription's own
  // currency field, so this never depends on the two staying in sync.
  const displayCurrency = Object.keys(catalog[0]?.prices ?? {})[0] ?? "USD"

  const selected = Object.fromEntries(
    attached.map((a) => [a.addon_id, a.quantity])
  )

  function handlePickerCommit(staged: Record<string, number>) {
    const diff = computeAmendmentDiff(staged, attached)
    if (!diff.immediate.length && !diff.scheduled.length) return
    if (isTrialing) {
      if (diff.scheduled.length > 0) setTrialConfirmDiff(diff)
      else void applyAmendmentDiff(diff, attach, detach)
    } else {
      setReviewStaged(staged)
    }
  }

  if (catalogLoading || attachedLoading) {
    return (
      <Card>
        <CardHeader>
          <Skeleton className="h-5 w-24" />
        </CardHeader>
        <CardContent className="flex flex-col gap-2">
          {[1, 2].map((i) => (
            <Skeleton key={i} className="h-8 w-full" />
          ))}
        </CardContent>
      </Card>
    )
  }

  if (!catalog.length) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>{t("billing.addons.title")}</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-muted-foreground">
            {t("billing.addons.catalogEmpty")}
          </p>
        </CardContent>
      </Card>
    )
  }

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>{t("billing.addons.title")}</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <OverageWarningCard organizationId={organizationId} />

          {!isOwner && (
            <p className="text-xs text-muted-foreground">
              {t("billing.managedByNote")}
            </p>
          )}

          {attached.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              {t("billing.addons.noneAttached")}
            </p>
          ) : (
            <ul className="flex flex-col gap-3">
              {attached.map((addonRow) => {
                const catalogAddon = catalogById.get(addonRow.addon_id)
                const prices = catalogAddon?.prices[displayCurrency]
                const amount = prices
                  ? cycle === "monthly"
                    ? prices.monthly
                    : prices.yearly
                  : 0
                const scheduledRemoval = addonRow.scheduled_quantity === 0
                return (
                  <li
                    key={addonRow.addon_id}
                    className="flex items-center justify-between gap-3 text-sm"
                  >
                    <div className="flex flex-col gap-1">
                      <span className="font-medium">
                        {addonRow.name}
                        <span className="ml-2 rounded-full bg-emerald-500/10 px-2 py-0.5 text-xs font-medium text-emerald-600">
                          {t("billing.addons.activeQty", {
                            qty: addonRow.quantity,
                          })}
                        </span>
                      </span>
                      <span className="text-xs text-muted-foreground">
                        {catalogAddon?.description} ·{" "}
                        {formatPrice(amount, displayCurrency, cycle, t)}
                      </span>
                      {addonRow.scheduled_quantity != null && (
                        <div className="flex items-center gap-1.5">
                          <Badge variant="outline">
                            {scheduledRemoval
                              ? t("billing.addons.scheduledRemovalBadge", {
                                  date: formatDate(periodEnd),
                                })
                              : t("billing.addons.scheduledQuantityBadge", {
                                  quantity: addonRow.scheduled_quantity,
                                  date: formatDate(periodEnd),
                                })}
                          </Badge>
                          {isOwner && (
                            <Button
                              variant="ghost"
                              size="icon"
                              className="size-6"
                              title={t("common.undo")}
                              aria-label={t("common.undo")}
                              disabled={undoingSchedule}
                              onClick={() =>
                                undoScheduledChange(addonRow.addon_id)
                              }
                            >
                              <IconArrowBackUp className="size-3.5" />
                            </Button>
                          )}
                        </div>
                      )}
                      {addonRow.pending_quantity != null && (
                        <div className="flex items-center gap-1.5">
                          <Badge variant="outline">
                            {t("billing.addons.pendingQuantityBadge", {
                              quantity: addonRow.pending_quantity,
                            })}
                          </Badge>
                          {isOwner &&
                            addonRow.pending_invoice_id &&
                            (() => {
                              const pendingInvoice = invoicesById.get(
                                addonRow.pending_invoice_id
                              )
                              return (
                                pendingInvoice && (
                                  <PayButton
                                    invoiceId={pendingInvoice.id}
                                    organizationId={organizationId}
                                    amountCents={pendingInvoice.amount_cents}
                                    currency={pendingInvoice.currency}
                                  />
                                )
                              )
                            })()}
                        </div>
                      )}
                    </div>
                  </li>
                )
              })}
            </ul>
          )}

          {isOwner && (
            <Button
              size="sm"
              variant="outline"
              className="self-start"
              onClick={() => setPickerRequested(true)}
            >
              {t("billing.addons.manage")}
            </Button>
          )}
        </CardContent>
      </Card>

      <AddAddonsDialog
        open={pickerRequested}
        onOpenChange={(open) => {
          if (!open) setPickerRequested(false)
        }}
        cycle={cycle}
        selected={selected}
        onChange={handlePickerCommit}
        organizationId={organizationId}
      />

      {reviewStaged && (
        <ReviewChangesDialog
          open
          onOpenChange={(open) => {
            if (!open) setReviewStaged(null)
          }}
          staged={reviewStaged}
          currentlyAttached={attached}
          catalog={catalog}
          periodEnd={periodEnd}
          organizationId={organizationId}
        />
      )}

      {trialConfirmDiff && (
        <ConfirmationDialog
          open
          onOpenChange={(open) => {
            if (!open) {
              setTrialConfirmDiff(null)
              setTrialConfirmFailed([])
            }
          }}
          render={<span className="hidden" />}
          nativeButton={false}
          title={t("billing.scheduledAmendments.trialConfirm.title")}
          description={
            trialConfirmFailed.length > 0
              ? t("billing.scheduledAmendments.reviewChanges.partialFailure")
              : t("billing.scheduledAmendments.trialConfirm.description")
          }
          consequences={trialConfirmDiff.scheduled.map((c) =>
            t("billing.scheduledAmendments.reviewChanges.changeLine", {
              name:
                attached.find((a) => a.addon_id === c.addonId)?.name ??
                c.addonId,
              from: c.fromQty,
              to: c.toQty,
            })
          )}
          confirmLabel={t("billing.scheduledAmendments.trialConfirm.confirm")}
          destructive={false}
          pending={trialConfirmSubmitting || attaching || detaching}
          onConfirm={() => {
            setTrialConfirmSubmitting(true)
            void applyAmendmentDiff(trialConfirmDiff, attach, detach).then(
              (stillFailed) => {
                setTrialConfirmSubmitting(false)
                setTrialConfirmFailed(stillFailed)
                if (stillFailed.length === 0) setTrialConfirmDiff(null)
              }
            )
          }}
        >
          {null}
        </ConfirmationDialog>
      )}
    </>
  )
}
