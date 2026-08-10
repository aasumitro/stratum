import { useTranslation } from "react-i18next"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { formatPrice } from "@/features/billing/utils"
import { slugify } from "@/lib/format"
import { cn } from "@/lib/ui"
import type { OrganizationCreateForm } from "@/features/organization/hooks/use-organization-create-form"
import type { BillingCycle } from "@/types/billing"

interface Props {
  formState: OrganizationCreateForm
  /** onboarding's 3-step wizard (Details -> Plan -> Review) renders one
   * section at a time; the "create another organization" sheet renders
   * everything together in one scroll ("all"). Plan applies to every
   * organization now, not just the first — see use-organization-create-form.ts. */
  section?: "details" | "plan" | "all"
}

/**
 * Shared name/slug/plan fields for organization creation — rendered
 * inside both the onboarding step and the "create another organization"
 * sheet. See use-organization-create-form.ts for why this is shared.
 */
export function OrganizationFormFields({ formState, section = "all" }: Props) {
  const { t } = useTranslation()
  const { form, plans, plansLoading, isFirstOrganization } = formState
  const showDetails = section === "details" || section === "all"
  const showPlan = section === "plan" || section === "all"

  return (
    <>
      {showDetails && (
        <>
          <form.Field
            name="name"
            validators={{
              onChange: ({ value }) =>
                !value.trim()
                  ? t("onboarding.organization.nameRequired")
                  : undefined,
            }}
          >
            {(field) => (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="ws_name">
                  {t("onboarding.organization.organizationName")}
                </Label>
                <Input
                  id="ws_name"
                  placeholder={t(
                    "onboarding.organization.organizationNamePlaceholder"
                  )}
                  value={field.state.value}
                  onBlur={field.handleBlur}
                  onChange={(e) => {
                    field.handleChange(e.target.value)
                    form.setFieldValue("slug", slugify(e.target.value))
                  }}
                />
                {field.state.meta.isTouched &&
                  field.state.meta.errors.length > 0 && (
                    <p className="text-xs text-destructive">
                      {field.state.meta.errors[0]}
                    </p>
                  )}
              </div>
            )}
          </form.Field>

          <form.Field
            name="slug"
            validators={{
              onChange: ({ value }) => {
                if (!value.trim())
                  return t("onboarding.organization.slugRequired")
                if (!/^[a-z0-9-]+$/.test(value))
                  return t("onboarding.organization.slugInvalid")
                return undefined
              },
            }}
          >
            {(field) => (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="ws_slug">
                  {t("onboarding.organization.slug")}
                </Label>
                <Input
                  id="ws_slug"
                  placeholder={t("onboarding.organization.slugPlaceholder")}
                  value={field.state.value}
                  onBlur={field.handleBlur}
                  onChange={(e) => field.handleChange(slugify(e.target.value))}
                />
                {field.state.meta.isTouched &&
                  field.state.meta.errors.length > 0 && (
                    <p className="text-xs text-destructive">
                      {field.state.meta.errors[0]}
                    </p>
                  )}
                <p className="text-xs text-muted-foreground">
                  {t("onboarding.organization.slugHint")}
                </p>
              </div>
            )}
          </form.Field>
        </>
      )}

      {showPlan && (
        <div className="flex flex-col gap-2">
          <div className="flex items-center justify-between gap-2">
            <Label>{t("billing.plans.title")}</Label>
            <div className="flex items-center gap-2">
              {/* Entry point for the full pricing-comparison page — deferred
                  in ROADMAP.md (needs an unauthenticated catalog route +
                  rate limiting first), so this stays disabled until that page exists. */}
              <button
                type="button"
                disabled
                title={t("onboarding.organization.comparePlansHint")}
                className="text-xs font-medium text-muted-foreground/50 underline"
              >
                {t("onboarding.organization.comparePlans")}
              </button>
              <form.Field name="cycle">
                {(cycleField) => (
                  <div className="flex rounded-lg border p-0.5 text-xs">
                    {(["monthly", "yearly"] as const).map((c) => (
                      <button
                        key={c}
                        type="button"
                        onClick={() => cycleField.handleChange(c)}
                        className={cn(
                          "rounded-md px-3 py-1 font-medium capitalize transition-colors",
                          cycleField.state.value === c
                            ? "bg-primary text-primary-foreground"
                            : "text-muted-foreground hover:text-foreground"
                        )}
                      >
                        {t(`billing.plans.${c}`)}
                      </button>
                    ))}
                  </div>
                )}
              </form.Field>
            </div>
          </div>

          <form.Field
            name="plan"
            validators={{
              onChange: ({ value }) =>
                !value ? t("billing.plans.selectionRequired") : undefined,
            }}
          >
            {(field) => (
              <div className="flex flex-col gap-2">
                {plansLoading ? (
                  <div className="flex flex-col gap-2">
                    {[1, 2, 3].map((i) => (
                      <Skeleton key={i} className="h-16 w-full rounded-xl" />
                    ))}
                  </div>
                ) : (
                  <form.Subscribe selector={(s) => s.values.cycle}>
                    {(cycle) => (
                      <RadioGroup
                        value={field.state.value}
                        onValueChange={(v) => field.handleChange(v ?? "solo")}
                        className="flex flex-col gap-2"
                      >
                        {plans.map((plan) => {
                          // The API always scopes `prices` down to a single,
                          // server-resolved currency (see usePlans in
                          // billing/hooks.ts) — read whichever one it sent
                          // back instead of assuming "USD".
                          const [currency, prices] = Object.entries(
                            plan.prices
                          )[0] ?? ["USD", undefined]
                          const amount =
                            cycle === "yearly"
                              ? (prices?.yearly ?? 0)
                              : (prices?.monthly ?? 0)
                          return (
                            <label
                              key={plan.id}
                              className={cn(
                                "flex cursor-pointer items-start gap-3 rounded-xl border p-3.5 text-sm transition-colors",
                                field.state.value === plan.id
                                  ? "border-primary shadow-sm"
                                  : "hover:bg-accent"
                              )}
                            >
                              <RadioGroupItem
                                value={plan.id}
                                className="mt-0.5"
                              />
                              <div className="flex flex-1 flex-col gap-0.5">
                                <div className="flex items-center justify-between gap-2">
                                  <span className="font-semibold">
                                    {plan.name}
                                  </span>
                                  <span className="text-sm font-bold">
                                    {formatPrice(
                                      amount,
                                      currency,
                                      cycle as BillingCycle,
                                      t
                                    )}
                                  </span>
                                </div>
                                <span className="text-xs text-muted-foreground">
                                  {plan.description}
                                </span>
                                <span className="text-xs text-muted-foreground">
                                  {isFirstOrganization
                                    ? t("onboarding.organization.trialBadge")
                                    : t("onboarding.organization.noTrialBadge")}
                                </span>
                              </div>
                            </label>
                          )
                        })}
                      </RadioGroup>
                    )}
                  </form.Subscribe>
                )}

                {field.state.meta.isTouched &&
                  field.state.meta.errors.length > 0 && (
                    <p className="text-xs text-destructive">
                      {field.state.meta.errors[0]}
                    </p>
                  )}
              </div>
            )}
          </form.Field>

          {!plansLoading && (
            <div className="flex items-center gap-2 rounded-xl border p-3 text-sm text-muted-foreground">
              <span className="size-3.5 shrink-0 rounded-full border border-muted-foreground/40" />
              <span className="font-semibold text-foreground">
                {t("onboarding.organization.customPlanName")}
              </span>
              <span className="text-xs">
                {t("onboarding.organization.customPlanDescription")}
              </span>
              <span className="flex-1" />
              <span className="text-xs font-semibold text-foreground underline">
                {t("onboarding.organization.customPlanPrice")}
              </span>
            </div>
          )}
        </div>
      )}
    </>
  )
}
