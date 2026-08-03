import { useState, useMemo } from "react"
import { useTranslation, Trans } from "react-i18next"
import { IconLoader2, IconAlertTriangle } from "@tabler/icons-react"
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
  useDowngradeSubscription,
  useInvoicePreview,
} from "@/features/billing/hooks"
import { useOrganization } from "@/features/organization/hooks/use-organization"
import { useOrganizationMembers } from "@/features/organization/hooks/use-members"
import { usePermissions } from "@/hooks/use-permissions"
import { formatPrice } from "@/features/billing/utils"
import { formatMoney } from "@/lib/format"
import type {
  BillingCycle,
  InvoicePreview,
  OverageResolution,
  SubscriptionStatus,
} from "@/types/billing"
import type { Member } from "@/types/organization"
import { InvoicePreviewNote } from "./invoice-preview-note"

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
  open: boolean
  onOpenChange: (open: boolean) => void
  targetPlan: string
  targetCycle: BillingCycle
  currentPlan: string
  currentCycle: BillingCycle
  currentPeriodEnd?: string
  currency: string
  subscriptionStatus: SubscriptionStatus
  onBackToPlans: () => void
}

type Step = "preview" | "selection" | "review" | "success"

export function DowngradeWizard({
  organizationId,
  open,
  onOpenChange,
  targetPlan,
  targetCycle,
  currentPlan,
  currentCycle,
  currentPeriodEnd,
  currency,
  subscriptionStatus,
  onBackToPlans,
}: Props) {
  const { t } = useTranslation()
  // Trialing has no paid period to protect, so the backend applies a
  // downgrade immediately and resolves overage synchronously — every other
  // status defers to renewal, doesn't touch live plan/cycle or entitlement
  // yet, and ignores any member/file picks sent along (nothing is being
  // removed today), so this wizard skips the selection step and swaps the
  // destructive/proration copy for a plain scheduled-effective note
  // whenever this is false.
  const isTrialing = subscriptionStatus === "trialing"
  const [step, setStep] = useState<Step>("preview")
  // Frozen at confirm time — see UpgradeWizard's identical field for why
  // (the preview query's enabled condition goes false once step flips to
  // "success", so a live read risks showing stale/refetched data instead).
  const [confirmedPreview, setConfirmedPreview] =
    useState<InvoicePreview | null>(null)
  // Same freeze-at-confirm reasoning as confirmedPreview above, but for
  // whether a pending invoice existed going in — changePlanWithMetadata
  // (backend, called internally by downgradeSubscription) voids it and
  // issues a fresh one when it does, so the success copy must reflect that
  // instead of the plain proration text.
  const [hadPendingInvoiceAtConfirm, setHadPendingInvoiceAtConfirm] =
    useState(false)

  // Selections
  const [selectedMembers, setSelectedMembers] = useState<string[]>([])
  const [selectedFiles, setSelectedFiles] = useState<string[]>([])

  // What the backend actually removed, captured from the downgrade
  // response for the success step — distinct from the pre-submit
  // selection state above, which only reflects what the owner picked.
  const [result, setResult] = useState<OverageResolution | null>(null)

  // Real mutation
  const { mutate: downgrade, isPending: downgrading } =
    useDowngradeSubscription(organizationId)

  const { hasPendingInvoice } = usePermissions()
  // country_code, not the subscription's own currency field, is what scopes
  // this fetch down to one currency — kept disabled until it's known,
  // rather than firing once unscoped and again once scoped.
  const { data: orgData, isLoading: orgLoading } =
    useOrganization(organizationId)
  const countryCode = orgData?.data?.country_code ?? ""
  const { data: plansData } = usePlans(countryCode, !orgLoading)
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
  const cycleChanged = targetCycle !== currentCycle

  // fetch members for selection
  const { data: membersData } = useOrganizationMembers(organizationId, {
    enabled: step === "selection",
  })
  const members = membersData?.data ?? []

  // Preview data (refetched when entering review step)
  const { data: previewData, isFetching: previewLoading } = useInvoicePreview(
    organizationId,
    targetPlan,
    targetCycle,
    open && (step === "preview" || step === "review")
  )
  const preview = previewData?.data
  const overage = preview?.overage

  const hasOverage = useMemo(() => {
    if (!overage) return false
    return (
      (overage?.members && overage.members.current > overage.members.allowed) ||
      (overage?.storage && overage.storage.current > overage.storage.allowed)
    )
  }, [overage])

  // overage.members.auto_select_removals always reflects a zero-selection
  // preview (the backend never receives the owner's in-progress picks until
  // final submit), so it can't just be concatenated with selectedMembers —
  // that double-lists anyone the owner already picked and overstates who
  // will actually be removed. Reconstruct what the backend will actually do
  // once it excludes the manual picks: cap the manual list to how many
  // removals are actually needed (matching the backend's own trim), then
  // fill the remainder from the auto list with manual picks subtracted out.
  const membersToRemove = useMemo(() => {
    const needed = Math.max(
      (overage?.members?.current ?? 0) - (overage?.members?.allowed ?? 0),
      0
    )
    const manual = selectedMembers.slice(0, needed)
    const auto = (overage?.members?.auto_select_removals ?? [])
      .filter((m) => !manual.includes(m))
      .slice(0, needed - manual.length)
    return { manual, auto }
  }, [overage, selectedMembers])

  // Move forward from preview — the selection step only makes sense while
  // trialing (the only status where a downgrade removes anything today).
  function handleContinueFromPreview() {
    if (isTrialing && hasOverage) {
      setStep("selection")
    } else {
      setStep("review")
    }
  }

  function handleConfirm() {
    downgrade(
      {
        plan: targetPlan,
        cycle: targetCycle,
        preferred_member_auth_subs: selectedMembers,
        preferred_file_ids: selectedFiles,
      },
      {
        onSuccess: (data) => {
          const overage = data.data?.overage
          // A Go nil slice marshals to JSON null, not [] — any dimension
          // with nothing removed (e.g. no files touched at all) comes back
          // null here, not empty. Normalize once, on the way in, so every
          // consumer below can treat these as plain arrays.
          setResult(
            overage
              ? {
                  removed_member_auth_subs:
                    overage.removed_member_auth_subs ?? [],
                  auto_selected_member_subs:
                    overage.auto_selected_member_subs ?? [],
                  removed_file_ids: overage.removed_file_ids ?? [],
                  auto_selected_file_ids: overage.auto_selected_file_ids ?? [],
                }
              : null
          )
          setConfirmedPreview(preview ?? null)
          setHadPendingInvoiceAtConfirm(hasPendingInvoice)
          setStep("success")
        },
      }
    )
  }

  function handleClose() {
    onOpenChange(false)
    // reset after animation
    setTimeout(() => {
      setStep("preview")
      setSelectedMembers([])
      setSelectedFiles([])
      setResult(null)
      setConfirmedPreview(null)
      setHadPendingInvoiceAtConfirm(false)
    }, 300)
  }

  if (step === "success") {
    return (
      <Dialog open={open} onOpenChange={handleClose}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              {isTrialing
                ? t("billing.downgrade.successTitle")
                : t("billing.downgrade.scheduledSuccessTitle")}
            </DialogTitle>
            <DialogDescription>
              {isTrialing
                ? t("billing.downgrade.successDescription")
                : t("billing.downgrade.scheduledSuccessDescription", {
                    plan: targetPlanInfo?.name ?? targetPlan,
                    date: formatDate(currentPeriodEnd),
                  })}
            </DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-2 rounded-lg bg-muted p-3 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {t("billing.downgrade.planLabel")}
              </span>
              <span className="font-medium">
                {targetPlanInfo?.name ?? targetPlan}
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {t("billing.downgrade.cycleLabel")}
              </span>
              <span className="font-medium">
                {t(`billing.plans.${targetCycle}`)}
              </span>
            </div>
            {isTrialing &&
              (hadPendingInvoiceAtConfirm && confirmedPreview ? (
                <p className="pt-1 text-xs text-muted-foreground">
                  {t("billing.downgrade.successNewInvoiceIssued", {
                    amount: formatMoney(
                      confirmedPreview.total_cents,
                      confirmedPreview.currency
                    ),
                  })}
                </p>
              ) : (
                confirmedPreview?.new_period_end && (
                  <p className="pt-1 text-xs text-muted-foreground">
                    {t("billing.plans.noChargeToday")}{" "}
                    {t("billing.downgrade.successEffectiveOn", {
                      date: formatDate(confirmedPreview.new_period_end),
                    })}
                  </p>
                )
              ))}
          </div>

          {isTrialing && (
            <div className="py-4 text-sm text-muted-foreground">
              {result &&
              (result.removed_member_auth_subs.length > 0 ||
                result.removed_file_ids.length > 0) ? (
                <div className="flex flex-col gap-2">
                  <p className="font-medium text-foreground">
                    {t("billing.downgrade.successRemovedInfo")}
                  </p>
                  <ul className="list-inside list-disc">
                    {result.removed_member_auth_subs.map((m) => (
                      <li key={m}>
                        {m}
                        {result.auto_selected_member_subs.includes(m) && (
                          <> {t("billing.downgrade.autoSelected")}</>
                        )}
                      </li>
                    ))}
                    {result.removed_file_ids.map((f) => (
                      <li key={f}>
                        {f}
                        {result.auto_selected_file_ids.includes(f) && (
                          <> {t("billing.downgrade.autoSelected")}</>
                        )}
                      </li>
                    ))}
                  </ul>
                </div>
              ) : (
                <p>{t("billing.downgrade.successNothingRemoved")}</p>
              )}
            </div>
          )}
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
            <DialogTitle>{t("billing.downgrade.reviewTitle")}</DialogTitle>
            <DialogDescription>
              {t("billing.downgrade.reviewDescription")}
            </DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-2 rounded-lg bg-muted p-3 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {t("billing.downgrade.currentPlanLabel")}
              </span>
              <span className="font-medium">
                {currentPlanInfo?.name ?? currentPlan}
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {t("billing.downgrade.selectedPlanLabel")}
              </span>
              <span className="font-medium">
                {targetPlanInfo?.name ?? targetPlan}
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {t("billing.downgrade.cycleLabel")}
              </span>
              <span className="font-medium">
                {cycleChanged
                  ? `${t(`billing.plans.${currentCycle}`)} → ${t(`billing.plans.${targetCycle}`)}`
                  : t(`billing.plans.${targetCycle}`)}
              </span>
            </div>
          </div>

          {isTrialing ? (
            <InvoicePreviewNote
              preview={preview}
              loading={previewLoading}
              currentPeriodEnd={currentPeriodEnd}
              planName={targetPlanInfo?.name ?? targetPlan}
              hasPendingInvoice={hasPendingInvoice}
            />
          ) : (
            <p className="rounded-lg bg-muted p-3 text-sm text-muted-foreground">
              <b className="text-foreground">
                {t("billing.plans.noChargeToday")}
              </b>{" "}
              {t("billing.downgrade.scheduledReviewNote", {
                date: formatDate(currentPeriodEnd),
              })}
            </p>
          )}

          {isTrialing &&
            (previewLoading ? (
              <div className="py-4">
                <Skeleton className="h-20 w-full" />
              </div>
            ) : (
              <div className="flex flex-col gap-4 py-4 text-sm">
                <div className="rounded-md border border-destructive/20 bg-destructive/10 p-3 text-destructive">
                  <div className="flex items-start gap-2">
                    <IconAlertTriangle className="mt-0.5 h-5 w-5 shrink-0" />
                    <div>
                      <p className="font-semibold">
                        {t("billing.downgrade.warningTitle")}
                      </p>
                      <p className="mt-1">
                        {t("billing.downgrade.warningBody")}
                      </p>
                    </div>
                  </div>
                </div>

                {hasOverage && (
                  <div className="rounded-md border p-3">
                    <p className="font-medium">
                      {t("billing.downgrade.toBeRemoved")}
                    </p>
                    <ul className="mt-2 list-inside list-disc text-muted-foreground">
                      {membersToRemove.manual.map((m) => (
                        <li key={m}>{m}</li>
                      ))}
                      {membersToRemove.auto.map((m) => (
                        <li key={m}>
                          {m} {t("billing.downgrade.autoSelected")}
                        </li>
                      ))}
                      {selectedFiles.map((f: string) => (
                        <li key={f}>{f}</li>
                      ))}
                      {overage?.storage?.auto_select_removals?.map(
                        (f: string) => (
                          <li key={f}>
                            {f} {t("billing.downgrade.autoSelected")}
                          </li>
                        )
                      )}
                    </ul>
                  </div>
                )}
              </div>
            ))}

          <DialogFooter>
            <Button
              variant="outline"
              onClick={() =>
                isTrialing && hasOverage
                  ? setStep("selection")
                  : onBackToPlans()
              }
              disabled={downgrading}
            >
              {t("common.back")}
            </Button>
            <Button
              variant={isTrialing ? "destructive" : "default"}
              onClick={handleConfirm}
              disabled={downgrading || (isTrialing && previewLoading)}
            >
              {downgrading && (
                <IconLoader2
                  data-icon="inline-start"
                  className="animate-spin"
                />
              )}
              {t("billing.downgrade.confirmDowngrade")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    )
  }

  if (step === "selection") {
    return (
      <Dialog open={open} onOpenChange={handleClose}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("billing.downgrade.selectionTitle")}</DialogTitle>
            <DialogDescription>
              <Trans
                i18nKey="billing.downgrade.autoFillDisclosure"
                components={{ b: <b /> }}
              />
            </DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-4 py-4 text-sm">
            {overage?.members &&
              overage.members.current > overage.members.allowed && (
                <div className="flex flex-col gap-2">
                  <p className="font-semibold">
                    {t("billing.downgrade.selectMembersToRemove")}
                  </p>
                  <div className="max-h-40 overflow-y-auto rounded-md border p-2">
                    {members
                      .filter((m: Member) => m.role !== "owner")
                      .map((m: Member) => (
                        <label
                          key={m.auth_sub}
                          className="flex items-center gap-2 p-1"
                        >
                          <input
                            type="checkbox"
                            className="accent-primary"
                            checked={selectedMembers.includes(m.auth_sub)}
                            onChange={(e) => {
                              if (e.target.checked) {
                                setSelectedMembers((prev) => [
                                  ...prev,
                                  m.auth_sub,
                                ])
                              } else {
                                setSelectedMembers((prev) =>
                                  prev.filter((id) => id !== m.auth_sub)
                                )
                              }
                            }}
                          />
                          <span>{m.email || m.auth_sub}</span>
                        </label>
                      ))}
                  </div>
                </div>
              )}

            {overage?.storage &&
              overage.storage.current > overage.storage.allowed && (
                <div className="flex flex-col gap-2">
                  <p className="font-semibold">
                    {t("billing.downgrade.selectFilesToRemove")}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {t("billing.downgrade.filesSelectionNotImplementedYet")}
                  </p>
                </div>
              )}
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => onBackToPlans()}>
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

  // default to preview step
  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("billing.downgrade.previewTitle")}</DialogTitle>
          <DialogDescription>
            {t("billing.downgrade.previewDescription")}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-2 rounded-lg bg-muted p-3 text-sm">
          <div className="flex items-center justify-between">
            <span className="text-muted-foreground">
              {t("billing.downgrade.currentPlanLabel")}
            </span>
            <span className="font-medium">
              {currentPlanInfo?.name ?? currentPlan}
            </span>
          </div>
          <div className="flex items-center justify-between">
            <span className="text-muted-foreground">
              {t("billing.downgrade.selectedPlanLabel")}
            </span>
            <span className="font-medium">
              {targetPlanInfo?.name ?? targetPlan}
            </span>
          </div>
          {targetPrices && (
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {t("billing.downgrade.newPrice")}
              </span>
              <span className="font-medium">
                {formatPrice(
                  targetCycle === "monthly"
                    ? targetPrices.monthly
                    : targetPrices.yearly,
                  displayCurrency,
                  targetCycle,
                  t
                )}
              </span>
            </div>
          )}
          <div className="flex items-center justify-between">
            <span className="text-muted-foreground">
              {t("billing.downgrade.cycleLabel")}
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

        <p className="text-sm text-muted-foreground">
          {isTrialing
            ? t("billing.downgrade.previewOverageWarning")
            : t("billing.downgrade.previewScheduledNote")}
        </p>

        <DialogFooter>
          <Button variant="outline" onClick={onBackToPlans}>
            {t("common.back")}
          </Button>
          <Button onClick={handleContinueFromPreview} disabled={previewLoading}>
            {t("common.continue")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
