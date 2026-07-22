import type { BillingCycle } from "@/types/billing"

export function formatPrice(
  amount: number,
  currency: string,
  cycle: BillingCycle,
  t: (k: string) => string
): string {
  const suffix =
    cycle === "monthly"
      ? t("billing.plans.perMonth")
      : t("billing.plans.perYear")
  if (currency === "IDR") return `Rp${amount.toLocaleString("id-ID")}${suffix}`
  return `$${(amount / 100).toLocaleString("en-US")}${suffix}`
}
