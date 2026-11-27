import type { BillingCycle } from "@/types/billing"
import type { Plan } from "@/types/reference"

// What a "too expensive" cancellation is offered instead, from the catalog the
// organization is billed in: the next lower priced plan on the same cycle, and
// yearly billing when it costs less than twelve months. No discount is made up.
export interface RetentionOffer {
  kind: "downgrade" | "yearly"
  plan: Plan
  cycle: BillingCycle
  // Amounts in the currency's minor unit (cents) or whole rupiah, as the
  // catalog gives them, per `cycle`.
  price: number
  instead: number
  saving: number
}

export function retentionOffers(
  plans: Plan[],
  currentPlanId: string,
  cycle: BillingCycle,
  currency: string
): RetentionOffer[] {
  const current = plans.find((p) => p.id === currentPlanId)
  const currentPrices = current?.prices[currency]
  if (!current || !currentPrices) return []
  const offers: RetentionOffer[] = []

  // The highest plan below this one (as the API orders a downgrade) that is
  // priced on this cycle and actually cheaper.
  const lower = plans
    .filter((p) => {
      const price = p.prices[currency]?.[cycle] ?? 0
      return (
        p.active &&
        p.sort_order < current.sort_order &&
        price > 0 &&
        price < currentPrices[cycle]
      )
    })
    .sort((a, b) => b.sort_order - a.sort_order)[0]
  if (lower) {
    const price = lower.prices[currency][cycle]
    offers.push({
      kind: "downgrade",
      plan: lower,
      cycle,
      price,
      instead: currentPrices[cycle],
      saving: currentPrices[cycle] - price,
    })
  }

  if (cycle === "monthly") {
    const yearly = currentPrices.yearly
    const twelveMonths = currentPrices.monthly * 12
    if (yearly > 0 && yearly < twelveMonths) {
      offers.push({
        kind: "yearly",
        plan: current,
        cycle: "yearly",
        price: yearly,
        instead: twelveMonths,
        saving: twelveMonths - yearly,
      })
    }
  }
  return offers
}
