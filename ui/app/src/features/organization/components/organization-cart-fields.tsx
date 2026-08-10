import { useState } from "react"
import { useSelector } from "@tanstack/react-store"
import { useTranslation } from "react-i18next"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { formatMoney } from "@/lib/format"
import {
  useAddonsCatalog,
  useEligibleCouponsForNewOrg,
} from "@/features/billing/hooks"
import { AddAddonsDialog } from "@/features/organization/components/add-addons-dialog"
import type { OrganizationCreateForm } from "@/features/organization/hooks/use-organization-create-form"
import type { BillingCycle } from "@/types/billing"

interface Props {
  formState: OrganizationCreateForm
}

/**
 * The create-organization "cart": an add-ons picker and a coupon code,
 * shared between onboarding's Review step and the "create another
 * organization" sheet. Both write straight into the create form's
 * `addons`/`coupon_code` fields — sent in the same POST /organizations
 * request, not applied afterward (see use-organization-create-form.ts).
 */
export function OrganizationCartFields({ formState }: Props) {
  const { t } = useTranslation()
  const { form } = formState
  const [addonDialogOpen, setAddonDialogOpen] = useState(false)

  const cycle = useSelector(form.store, (s) => s.values.cycle) as BillingCycle
  const selectedAddons = useSelector(form.store, (s) => s.values.addons)

  const { data: addonsCatalogData } = useAddonsCatalog()
  const addonsById = new Map(
    (addonsCatalogData?.data ?? []).map((a) => [a.id, a])
  )
  const { data: couponsData } = useEligibleCouponsForNewOrg()
  const coupons = couponsData?.data ?? []

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-2">
        <div className="flex items-center justify-between gap-2">
          <span className="text-sm font-semibold">
            {t("onboarding.organization.addonsSectionTitle")}
          </span>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setAddonDialogOpen(true)}
          >
            {t("onboarding.organization.addAddons")}
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">
          {t("onboarding.organization.addonsSectionSubtitle")}
        </p>

        {Object.keys(selectedAddons).length > 0 && (
          <ul className="flex flex-col divide-y rounded-lg border text-sm">
            {Object.entries(selectedAddons).map(([addonId, quantity]) => {
              const addon = addonsById.get(addonId)
              const [currency, prices] = Object.entries(
                addon?.prices ?? {}
              )[0] ?? ["USD", undefined]
              const amount =
                (cycle === "yearly" ? prices?.yearly : prices?.monthly) ?? 0
              return (
                <li
                  key={addonId}
                  className="flex items-center justify-between gap-3 px-3 py-2"
                >
                  <span>
                    {t("onboarding.organization.addonsLineFormat", {
                      name: addon?.name ?? addonId,
                      qty: quantity,
                    })}
                  </span>
                  <span className="font-medium">
                    {formatMoney(amount * quantity, currency)}
                  </span>
                </li>
              )
            })}
          </ul>
        )}
      </div>

      <AddAddonsDialog
        open={addonDialogOpen}
        onOpenChange={setAddonDialogOpen}
        cycle={cycle}
        selected={selectedAddons}
        onChange={(next) => form.setFieldValue("addons", next)}
      />

      <div className="flex flex-col gap-2">
        <Label htmlFor="coupon_code">
          {t("onboarding.organization.couponCodeLabel")}
        </Label>

        {coupons.length > 0 && (
          <ul className="flex flex-col gap-1.5 rounded-lg border p-2">
            {coupons.map((c) => (
              <li
                key={c.code}
                className="flex items-center justify-between gap-2 text-xs"
              >
                <div>
                  <span className="font-medium">{c.code}</span>{" "}
                  <span className="text-muted-foreground">
                    {c.discount_type === "percent"
                      ? `−${c.percent_off}%`
                      : `−${formatMoney(c.amount_cents ?? 0, c.currency ?? "USD")}`}{" "}
                    · {t(`billing.coupons.cadence.${c.cadence}`)}
                  </span>
                </div>
                <form.Subscribe
                  selector={(s) => s.values.coupon_code === c.code}
                >
                  {(applied) => (
                    <Button
                      type="button"
                      size="sm"
                      variant={applied ? "secondary" : "ghost"}
                      onClick={() => form.setFieldValue("coupon_code", c.code)}
                    >
                      {applied
                        ? t("onboarding.organization.couponApplied")
                        : t("billing.coupons.apply")}
                    </Button>
                  )}
                </form.Subscribe>
              </li>
            ))}
          </ul>
        )}

        <form.Field name="coupon_code">
          {(field) => (
            <Input
              id="coupon_code"
              placeholder={t("onboarding.organization.couponCodePlaceholder")}
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
            />
          )}
        </form.Field>
      </div>
    </div>
  )
}
