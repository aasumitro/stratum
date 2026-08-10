import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { IconCheck, IconLoader2 } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { useOrganizationCreateForm } from "@/features/organization/hooks/use-organization-create-form"
import { OrganizationFormFields } from "@/features/organization/components/organization-form-fields"
import { OrganizationCartFields } from "@/features/organization/components/organization-cart-fields"
import { OrganizationPriceSummary } from "@/features/organization/components/organization-price-summary"
import { useFeatures } from "@/features/billing/hooks"
import { formatPrice } from "@/features/billing/utils"
import { cn } from "@/lib/ui"
import type { BillingCycle } from "@/types/billing"
import type { Organization } from "@/types/organization"

export type InnerStep = "details" | "plan" | "review"

const TRIAL_DAYS = 7

export function CreateOrganizationForm({
  onCreated,
  onBack,
  onStepChange,
  hideStepIndicator = false,
}: {
  onCreated: (org: Organization) => void
  /** Exits the wizard entirely when triggered from the Details step. Later
   * steps handle their own Back locally and never call this. */
  onBack: () => void
  /** Fired whenever the inner step changes (including on mount) — lets a
   * container that doesn't otherwise track wizard progress (e.g. a dialog
   * sizing itself per step) stay in sync. */
  onStepChange?: (step: InnerStep) => void
  /** Hides the numbered step-dots row. The dots make sense as page-level
   * progress in onboarding's full-page wizard; inside a dialog the section
   * heading below already carries that context, so the dots are just
   * redundant chrome. */
  hideStepIndicator?: boolean
}) {
  const { t } = useTranslation()
  const [innerStep, setInnerStep] = useState<InnerStep>("details")

  useEffect(() => {
    onStepChange?.(innerStep)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [innerStep])
  // Gates the final Create button — plan/cycle are already required by the
  // API, this is a separate UI-only confirmation, not sent to the backend.
  const [agreedToTerms, setAgreedToTerms] = useState(false)

  const formState = useOrganizationCreateForm(onCreated)
  const { form, plans, isPending, serverError, isFirstOrganization } = formState
  // Lazy-initialized once per mount instead of computed inline during
  // render, which would call the impure Date.now() on every render.
  const [trialEndDate] = useState(
    () => new Date(Date.now() + TRIAL_DAYS * 24 * 60 * 60 * 1000)
  )

  const { data: featuresData } = useFeatures()
  const featuresById = new Map((featuresData?.data ?? []).map((f) => [f.id, f]))

  const STEPS: { key: InnerStep; label: string }[] = [
    { key: "details", label: t("onboarding.organization.stepDetails") },
    { key: "plan", label: t("onboarding.organization.stepPlan") },
    { key: "review", label: t("onboarding.organization.stepReview") },
  ]
  const stepIndex = STEPS.findIndex((s) => s.key === innerStep)

  const selectedPlan = plans.find((p) => p.id === form.state.values.plan)
  const cycle = form.state.values.cycle as BillingCycle
  // The API always scopes `prices` down to a single, server-resolved
  // currency (see usePlans in billing/hooks.ts) — read whichever one it
  // sent back instead of assuming "USD".
  const [currency, prices] = Object.entries(selectedPlan?.prices ?? {})[0] ?? [
    "USD",
    undefined,
  ]
  const amount =
    cycle === "yearly" ? (prices?.yearly ?? 0) : (prices?.monthly ?? 0)
  // Full benefit detail, not just a bare name — description plus, for
  // metered features, the plan's numeric limit (-1 = unlimited).
  const featureList = (selectedPlan?.features ?? []).map((id) => {
    const feature = featuresById.get(id)
    const limit = selectedPlan?.limits[id]
    const limitLabel =
      feature?.type === "metered" && limit !== undefined
        ? limit === -1
          ? t("onboarding.organization.reviewLimitUnlimited")
          : t("onboarding.organization.reviewLimitUpTo", { count: limit })
        : undefined
    return {
      id,
      name: feature?.name ?? id,
      description: feature?.description,
      limitLabel,
    }
  })
  const trialEndLabel = trialEndDate.toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        void form.handleSubmit()
      }}
      className={cn(
        "mx-auto flex w-full flex-col gap-4",
        // Review needs room for its two-column layout (plan/add-ons +
        // trial/price sidebar); Details/Plan stay narrower to match a
        // single-column form.
        innerStep === "review" ? "max-w-4xl" : "max-w-lg"
      )}
    >
      {!hideStepIndicator && (
        <div className="mb-12 flex items-center justify-center gap-3 text-sm">
          {STEPS.map((s, i) => {
            const done = i < stepIndex
            const active = i === stepIndex
            return (
              <div key={s.key} className="flex items-center gap-2.5">
                <span
                  className={cn(
                    "flex size-7 items-center justify-center rounded-full text-xs font-semibold transition-colors",
                    (done || active) && "bg-primary text-primary-foreground",
                    active && "ring-4 ring-primary/20",
                    !done && !active && "bg-muted text-muted-foreground"
                  )}
                >
                  {done ? <IconCheck className="size-3.5" /> : i + 1}
                </span>
                <span
                  className={cn(
                    active
                      ? "font-semibold text-foreground"
                      : "text-muted-foreground"
                  )}
                >
                  {s.label}
                </span>
                {i < STEPS.length - 1 && (
                  <span
                    className={cn(
                      "mx-1 h-px w-10 transition-colors",
                      done ? "bg-primary" : "bg-border"
                    )}
                  />
                )}
              </div>
            )
          })}
        </div>
      )}

      <div>
        <h2 className="text-xl font-extrabold">
          {t(`onboarding.organization.${innerStep}Title`)}
        </h2>
        <p className="mt-1 text-sm text-muted-foreground">
          {t(`onboarding.organization.${innerStep}Subtitle`)}
        </p>
      </div>

      {innerStep === "review" ? (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-[1fr_280px]">
          <div className="flex flex-col gap-4">
            <div className="flex flex-col gap-3 rounded-xl border p-4 text-sm">
              <span className="text-xs font-medium text-muted-foreground">
                {t("onboarding.organization.planSelectedLabel")}
              </span>
              <div>
                <span className="text-lg font-bold">{selectedPlan?.name}</span>
                <p className="text-xs text-muted-foreground capitalize">
                  {t(`billing.plans.${cycle}`)}
                </p>
              </div>

              {featureList.length > 0 && (
                <div className="flex flex-col gap-1.5 border-t pt-3">
                  <span className="text-xs font-medium text-muted-foreground">
                    {t("onboarding.organization.reviewWhatYouGet")}
                  </span>
                  <ul className="flex flex-col gap-1.5">
                    {featureList.map((feature) => (
                      <li
                        key={feature.id}
                        className="flex items-start gap-2 text-xs"
                      >
                        <IconCheck className="mt-0.5 size-3.5 shrink-0 text-emerald-500" />
                        <span className="flex flex-col">
                          <span>
                            {feature.name}
                            {feature.limitLabel && (
                              <span className="text-muted-foreground">
                                {" "}
                                — {feature.limitLabel}
                              </span>
                            )}
                          </span>
                          {feature.description && (
                            <span className="text-muted-foreground">
                              {feature.description}
                            </span>
                          )}
                        </span>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </div>

            <OrganizationCartFields formState={formState} />
          </div>

          <div className="flex flex-col gap-4">
            <div className="flex flex-col gap-2 rounded-xl border p-4 text-sm">
              <span className="text-xs text-muted-foreground">
                {isFirstOrganization
                  ? t("onboarding.organization.reviewTrialEyebrow")
                  : t("onboarding.organization.reviewNoTrial")}
              </span>

              {isFirstOrganization && (
                <div className="flex items-baseline gap-2">
                  <span className="text-2xl font-bold">
                    {t("onboarding.organization.reviewTrialDays", {
                      days: TRIAL_DAYS,
                    })}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {t("onboarding.organization.reviewTrialEnds", {
                      date: trialEndLabel,
                    })}
                  </span>
                </div>
              )}

              <div
                className={isFirstOrganization ? "border-t pt-2" : undefined}
              >
                <span className="text-xs text-muted-foreground">
                  {isFirstOrganization
                    ? t("onboarding.organization.reviewPriceAfterTrial")
                    : t("onboarding.organization.reviewPriceNow")}
                </span>
                <div className="text-xl font-bold">
                  {formatPrice(amount, currency, cycle, t)}
                </div>
              </div>

              <a
                href="#"
                aria-disabled="true"
                onClick={(e) => e.preventDefault()}
                className="pointer-events-none text-xs text-muted-foreground/50 underline"
              >
                {t("onboarding.organization.reviewHelpLink")}
              </a>
            </div>

            <OrganizationPriceSummary
              formState={formState}
              isFirstOrganization={isFirstOrganization}
            />
          </div>
        </div>
      ) : (
        <OrganizationFormFields formState={formState} section={innerStep} />
      )}

      {innerStep === "review" && (
        <label className="flex items-start gap-2">
          <Checkbox
            checked={agreedToTerms}
            onCheckedChange={(v) => setAgreedToTerms(v === true)}
            className="mt-0.5"
          />
          <span className="text-xs text-muted-foreground">
            {t("onboarding.organization.reviewAgreeTerms")}
          </span>
        </label>
      )}

      {serverError && (
        <p
          role="alert"
          className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {serverError}
        </p>
      )}

      {innerStep === "details" && (
        <div className="mt-2 flex gap-3">
          <Button
            type="button"
            variant="outline"
            onClick={onBack}
            className="flex-1"
          >
            {t("common.back")}
          </Button>
          <form.Subscribe
            selector={(s) =>
              s.values.name.trim().length > 0 &&
              /^[a-z0-9-]+$/.test(s.values.slug)
            }
          >
            {(detailsValid) => (
              <Button
                type="button"
                disabled={!detailsValid}
                className="flex-1"
                onClick={() => setInnerStep("plan")}
              >
                {t("common.continue")}
              </Button>
            )}
          </form.Subscribe>
        </div>
      )}

      {innerStep === "plan" && (
        <div className="mt-2 flex gap-3">
          <Button
            type="button"
            variant="outline"
            onClick={() => setInnerStep("details")}
            className="flex-1"
          >
            {t("common.back")}
          </Button>
          <form.Subscribe selector={(s) => s.values.plan.length > 0}>
            {(planValid) => (
              <Button
                type="button"
                disabled={!planValid}
                className="flex-1"
                onClick={() => setInnerStep("review")}
              >
                {t("common.continue")}
              </Button>
            )}
          </form.Subscribe>
        </div>
      )}

      {innerStep === "review" && (
        <div className="mt-2 flex gap-3">
          <Button
            type="button"
            variant="outline"
            onClick={() => setInnerStep("plan")}
            className="flex-1"
          >
            {t("common.back")}
          </Button>
          <Button
            type="submit"
            disabled={isPending || !agreedToTerms}
            className="flex-1"
          >
            {isPending && (
              <IconLoader2 data-icon="inline-start" className="animate-spin" />
            )}
            {t("onboarding.organization.createOrganization")}
          </Button>
        </div>
      )}
    </form>
  )
}
