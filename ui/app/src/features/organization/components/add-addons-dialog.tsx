import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconMinus, IconPlus } from "@tabler/icons-react"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { useAddonsCatalog, useOrgAddonsCatalog } from "@/features/billing/hooks"
import { formatMoney } from "@/lib/format"
import type { BillingCycle } from "@/types/billing"
import type { PlanPrices } from "@/types/reference"

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  cycle: BillingCycle
  /** addon_id -> quantity, owned by the create form's `addons` field
   * (use-organization-create-form.ts) */
  selected: Record<string, number>
  onChange: (next: Record<string, number>) => void
  /** When set, this dialog is showing an already-created organization's
   * catalog (billing/components/addons-section.tsx) — prices are scoped to
   * that subscription's own fixed currency. Omitted for the org-creation
   * cart below, where no subscription/currency exists yet. */
  organizationId?: string
}

// Used by organization-cart-fields.tsx wherever an organization is being
// created (onboarding's Review step, the "create another organization"
// sheet) — unlike billing/components/addons-section.tsx (settings page,
// attaches immediately via useAttachAddon), no organization exists yet. This
// only writes into the create form's `addons` field; the selection is sent
// in the same POST /organizations request, not a separate post-creation call.
//
// Every catalog addon shows a live −/qty/+ stepper (no separate "select"
// step) so the running total is always visible; "Add Selected" commits
// every addon with quantity > 0 at once, replacing the prior selection.
export function AddAddonsDialog({
  open,
  onOpenChange,
  cycle,
  selected,
  onChange,
  organizationId,
}: Props) {
  const { t } = useTranslation()
  const { data: refCatalogData, isLoading: refLoading } =
    useAddonsCatalog(!organizationId)
  const { data: orgCatalogData, isLoading: orgLoading } = useOrgAddonsCatalog(
    organizationId ?? "",
    !!organizationId
  )
  const catalogData = organizationId ? orgCatalogData : refCatalogData
  const isLoading = organizationId ? orgLoading : refLoading
  const catalog = (catalogData?.data ?? []).filter((a) => a.active)
  const [quantities, setQuantities] = useState<Record<string, number>>(selected)

  // Re-seed from the committed selection every time the dialog transitions
  // to open, so a Cancel never leaks uncommitted stepper adjustments into
  // the next open — setState-during-render, not an effect, per React's
  // "adjusting state when a prop changes" pattern (avoids the extra
  // re-render an effect would cause).
  const [wasOpen, setWasOpen] = useState(open)
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) setQuantities(selected)
  }

  function setQuantity(addonId: string, quantity: number) {
    setQuantities((prev) => ({ ...prev, [addonId]: Math.max(0, quantity) }))
  }

  function priceFor(addonPrices: Record<string, PlanPrices>) {
    const [currency, prices] = Object.entries(addonPrices)[0] ?? [
      "USD",
      undefined,
    ]
    const amount =
      cycle === "yearly" ? (prices?.yearly ?? 0) : (prices?.monthly ?? 0)
    return { currency, amount }
  }

  const total = catalog.reduce((sum, addon) => {
    const qty = quantities[addon.id] ?? 0
    return sum + priceFor(addon.prices).amount * qty
  }, 0)
  const totalCurrency = priceFor(catalog[0]?.prices ?? {}).currency

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            {t("onboarding.organization.addonsDialogTitle")}
          </DialogTitle>
          <DialogDescription>
            {t("onboarding.organization.addonsDialogDescription")}
          </DialogDescription>
        </DialogHeader>

        {isLoading ? (
          <div className="flex flex-col gap-2">
            {[1, 2].map((i) => (
              <Skeleton key={i} className="h-20 w-full rounded-xl" />
            ))}
          </div>
        ) : catalog.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            {t("onboarding.organization.addonsDialogEmpty")}
          </p>
        ) : (
          <ul className="flex flex-col gap-3">
            {catalog.map((addon) => {
              const { currency, amount } = priceFor(addon.prices)
              const qty = quantities[addon.id] ?? 0
              return (
                <li key={addon.id} className="rounded-xl border p-3.5 text-sm">
                  <div className="flex items-start justify-between gap-3">
                    <span className="font-semibold">{addon.name}</span>
                    <span className="text-right">
                      <span className="font-semibold">
                        {formatMoney(amount, currency)}
                      </span>
                      <span className="block text-xs text-muted-foreground">
                        {t("onboarding.organization.addonPerUnit")}
                      </span>
                    </span>
                  </div>
                  <p className="mt-0.5 text-xs text-muted-foreground">
                    {addon.description}
                  </p>

                  <div className="mt-3 flex items-center justify-between border-t pt-3">
                    <div className="flex items-center gap-1.5">
                      <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        className="size-8"
                        disabled={qty <= 0}
                        onClick={() => setQuantity(addon.id, qty - 1)}
                      >
                        <IconMinus className="size-3.5" />
                      </Button>
                      <span className="flex size-8 items-center justify-center rounded-md border text-sm">
                        {qty}
                      </span>
                      <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        className="size-8"
                        onClick={() => setQuantity(addon.id, qty + 1)}
                      >
                        <IconPlus className="size-3.5" />
                      </Button>
                    </div>
                    <span className="font-semibold">
                      {formatMoney(amount * qty, currency)}
                    </span>
                  </div>
                </li>
              )
            })}
          </ul>
        )}

        {catalog.length > 0 && (
          <div className="flex items-center justify-between border-t pt-3 text-sm">
            <span className="text-muted-foreground">
              {t("onboarding.organization.addonsTotalLabel")}
            </span>
            <span className="font-semibold">
              {formatMoney(total, totalCurrency)}
            </span>
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button
            onClick={() => {
              const next = Object.fromEntries(
                Object.entries(quantities).filter(([, qty]) => qty > 0)
              )
              onChange(next)
              onOpenChange(false)
            }}
          >
            {t("onboarding.organization.addSelected")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
