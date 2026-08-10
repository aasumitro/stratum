// Mirrors computeExtensionSubtotal (service_subscription_billing.go): every
// full 12-month block bills at the plan's yearly price, any remainder at
// the monthly price. months < 12 collapses to blocks == 0, today's flat
// calculation — same formula handles both, matching the backend exactly so
// this pre-commit estimate never disagrees with what's actually charged.
export function computeExtensionSubtotal(
  prices: { monthly: number; yearly: number } | undefined,
  months: number
): number {
  if (!prices) return 0
  const blocks = Math.floor(months / 12)
  const remainder = months % 12
  return blocks * prices.yearly + remainder * prices.monthly
}

// Mirrors computeExtensionAddonSubtotal (service_subscription_billing.go):
// the same block/remainder rule as computeExtensionSubtotal, applied to
// every currently attached addon and multiplied by its quantity — so the
// confirmation screen never shows a smaller number than what extending
// actually charges. Only live `quantity` counts, never
// scheduled_quantity/pending_quantity (not-yet-effective changes).
export function computeExtensionAddonSubtotal(
  addons: {
    quantity: number
    prices: Record<string, { monthly: number; yearly: number }>
  }[],
  currency: string,
  months: number
): number {
  const blocks = Math.floor(months / 12)
  const remainder = months % 12
  return addons.reduce((total, addon) => {
    const prices = addon.prices[currency]
    if (!prices) return total
    return (
      total +
      (blocks * prices.yearly + remainder * prices.monthly) * addon.quantity
    )
  }, 0)
}
