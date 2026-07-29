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
import { useOrganizationMembers } from "@/features/organization/hooks/use-members"
import { formatPrice } from "@/features/billing/utils"
import type {
  BillingCycle,
  InvoicePreview,
  OverageResolution,
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
  onBackToPlans,
}: Props) {
  const { t } = useTranslation()
  const [step, setStep] = useState<Step>("preview")
  // Frozen at confirm time — see UpgradeWizard's identical field for why
  // (the preview query's enabled condition goes false once step flips to
  // "success", so a live read risks showing stale/refetched data instead).
  const [confirmedPreview, setConfirmedPreview] =
    useState<InvoicePreview | null>(null)

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

  const { data: plansData } = usePlans()
  const plans = plansData?.data ?? []
  const targetPlanInfo = plans.find((p) => p.id === targetPlan)
  const currentPlanInfo = plans.find((p) => p.id === currentPlan)
  const targetPrices =
    targetPlanInfo?.prices[currency] ?? targetPlanInfo?.prices["USD"]
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

  // Move forward from preview
  function handleContinueFromPreview() {
    if (hasOverage) {
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
    }, 300)
  }

  if (step === "success") {
    return (
      <Dialog open={open} onOpenChange={handleClose}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("billing.downgrade.successTitle")}</DialogTitle>
            <DialogDescription>
              {t("billing.downgrade.successDescription")}
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
            {confirmedPreview?.new_period_end && (
              <p className="pt-1 text-xs text-muted-foreground">
                {t("billing.plans.noChargeToday")}{" "}
                {t("billing.downgrade.successEffectiveOn", {
                  date: formatDate(confirmedPreview.new_period_end),
                })}
              </p>
            )}
          </div>

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
                {t("billing.downgrade.planLabel")}
              </span>
              <span className="font-medium">
                {currentPlanInfo?.name ?? currentPlan} →{" "}
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
                    currency,
                    targetCycle,
                    t
                  )}
                </span>
              </div>
            )}
            {cycleChanged && (
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">
                  {t("billing.downgrade.cycleLabel")}
                </span>
                <span className="font-medium">
                  {t(`billing.plans.${currentCycle}`)} →{" "}
                  {t(`billing.plans.${targetCycle}`)}
                </span>
              </div>
            )}
          </div>

          <InvoicePreviewNote
            preview={preview}
            loading={previewLoading}
            currentPeriodEnd={currentPeriodEnd}
          />

          {previewLoading ? (
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
                    <p className="mt-1">{t("billing.downgrade.warningBody")}</p>
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
          )}

          <DialogFooter>
            <Button
              variant="outline"
              onClick={() =>
                hasOverage ? setStep("selection") : onBackToPlans()
              }
              disabled={downgrading}
            >
              {t("common.back")}
            </Button>
            <Button
              variant="destructive"
              onClick={handleConfirm}
              disabled={downgrading || previewLoading}
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
              {t("billing.downgrade.planLabel")}
            </span>
            <span className="font-medium">
              {currentPlanInfo?.name ?? currentPlan} →{" "}
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
                  currency,
                  targetCycle,
                  t
                )}
              </span>
            </div>
          )}
          {cycleChanged && (
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {t("billing.downgrade.cycleLabel")}
              </span>
              <span className="font-medium">
                {t(`billing.plans.${currentCycle}`)} →{" "}
                {t(`billing.plans.${targetCycle}`)}
              </span>
            </div>
          )}
        </div>

        <InvoicePreviewNote
          preview={preview}
          loading={previewLoading}
          currentPeriodEnd={currentPeriodEnd}
        />

        <p className="text-sm text-muted-foreground">
          {t("billing.downgrade.previewOverageWarning")}
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
