import { useSelector } from "@tanstack/react-store"
import { useTranslation } from "react-i18next"
import { formatMoney } from "@/lib/format"
import { formatPrice } from "@/features/billing/utils"
import {
  useAddonsCatalog,
  useEligibleCouponsForNewOrg,
} from "@/features/billing/hooks"
import type { OrganizationCreateForm } from "@/features/organization/hooks/use-organization-create-form"
import type { BillingCycle } from "@/types/billing"

interface Props {
  formState: OrganizationCreateForm
  isFirstOrganization: boolean
}

/**
 * Plan price + add-ons subtotal − coupon discount = total, computed from
 * the create form's current values — reused in the onboarding Review
 * step's sidebar and the "create another organization" sheet (single
 * column). Tax isn't shown: it's resolved server-side from the org's
 * country once it exists (contracts.ResolveCurrency / CountryTaxReader),
 * so there's nothing honest to compute here yet.
 */
export function OrganizationPriceSummary({
  formState,
  isFirstOrganization,
}: Props) {
  const { t } = useTranslation()
  const { form, plans } = formState

  const plan = useSelector(form.store, (s) => s.values.plan)
  const cycle = useSelector(form.store, (s) => s.values.cycle) as BillingCycle
  const countryCode = useSelector(form.store, (s) => s.values.country_code)
  const addons = useSelector(form.store, (s) => s.values.addons)
  const couponCode = useSelector(form.store, (s) => s.values.coupon_code)

  const selectedPlan = plans.find((p) => p.id === plan)
  const [currency, planPrices] = Object.entries(
    selectedPlan?.prices ?? {}
  )[0] ?? ["USD", undefined]
  const planAmount =
    (cycle === "yearly" ? planPrices?.yearly : planPrices?.monthly) ?? 0

  const { data: addonsCatalogData } = useAddonsCatalog(countryCode)
  const addonsById = new Map(
    (addonsCatalogData?.data ?? []).map((a) => [a.id, a])
  )
  const addonsTotal = Object.entries(addons).reduce((sum, [addonId, qty]) => {
    const prices = Object.values(addonsById.get(addonId)?.prices ?? {})[0]
    const amount = (cycle === "yearly" ? prices?.yearly : prices?.monthly) ?? 0
    return sum + amount * qty
  }, 0)

  const { data: couponsData } = useEligibleCouponsForNewOrg()
  const matchedCoupon = (couponsData?.data ?? []).find(
    (c) => c.code === couponCode
  )
  const subtotal = planAmount + addonsTotal
  const discount = matchedCoupon
    ? Math.min(
        matchedCoupon.discount_type === "percent"
          ? Math.round((subtotal * (matchedCoupon.percent_off ?? 0)) / 100)
          : (matchedCoupon.amount_cents ?? 0),
        subtotal
      )
    : 0
  const total = subtotal - discount

  if (!selectedPlan) return null

  return (
    <div className="flex flex-col gap-2 rounded-xl border p-4 text-sm">
      <span className="text-xs font-medium text-muted-foreground">
        {t("onboarding.organization.summaryTitle")}
      </span>

      <div className="flex items-center justify-between">
        <span className="text-muted-foreground">{selectedPlan.name}</span>
        <span>{formatMoney(planAmount, currency)}</span>
      </div>

      {addonsTotal > 0 && (
        <div className="flex items-center justify-between">
          <span className="text-muted-foreground">
            {t("onboarding.organization.summaryAddonsLabel")}
          </span>
          <span>{formatMoney(addonsTotal, currency)}</span>
        </div>
      )}

      {discount > 0 && (
        <div className="flex items-center justify-between text-emerald-600">
          <span>
            {t("onboarding.organization.summaryDiscountLabel", {
              code: matchedCoupon?.code,
            })}
          </span>
          <span>−{formatMoney(discount, currency)}</span>
        </div>
      )}

      <div className="flex items-center justify-between border-t pt-2">
        <span className="font-semibold">
          {isFirstOrganization
            ? t("onboarding.organization.reviewPriceAfterTrial")
            : t("onboarding.organization.reviewPriceNow")}
        </span>
        <span className="text-base font-bold">
          {formatPrice(total, currency, cycle, t)}
        </span>
      </div>

      <p className="text-xs text-muted-foreground">
        {t("onboarding.organization.summaryTaxNote")}
      </p>
    </div>
  )
}
