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
