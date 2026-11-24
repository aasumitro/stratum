import { describe, expect, it } from "vitest"
import type { Plan } from "@/types/reference"
import { retentionOffers } from "./retention-offer"

function plan(
  id: string,
  sort: number,
  monthly: number,
  yearly: number,
  active = true
) {
  return {
    id,
    name: id,
    sort_order: sort,
    active,
    prices: { USD: { monthly, yearly } },
  } as unknown as Plan
}

const plans = [
  plan("free", 0, 0, 0),
  plan("starter", 1, 1900, 19000),
  plan("legacy", 2, 2500, 25000, false),
  plan("pro", 3, 4900, 58800),
  plan("custom", 9, 0, 0),
]

describe("retentionOffers", () => {
  it("offers the next lower priced active plan, and yearly when it saves", () => {
    const offers = retentionOffers(plans, "pro", "monthly", "USD")
    expect(offers.map((o) => [o.kind, o.plan.id, o.price, o.saving])).toEqual([
      ["downgrade", "starter", 1900, 3000],
    ])
    const fromStarter = retentionOffers(plans, "starter", "monthly", "USD")
    expect(fromStarter.map((o) => [o.kind, o.saving])).toEqual([
      ["yearly", 3800],
    ])
  })

  it("skips a lower plan that is not cheaper for the next one that is", () => {
    const odd = [
      plan("starter", 1, 1900, 19000),
      plan("plus", 2, 5900, 59000),
      plan("pro", 3, 4900, 58800),
    ]
    const offers = retentionOffers(odd, "pro", "monthly", "USD")
    expect(offers[0]?.plan.id).toBe("starter")
  })

  it("offers nothing below the lowest priced plan or for an unknown plan", () => {
    expect(retentionOffers(plans, "starter", "yearly", "USD")).toEqual([])
    expect(retentionOffers(plans, "nope", "monthly", "USD")).toEqual([])
    expect(retentionOffers(plans, "pro", "monthly", "IDR")).toEqual([])
  })
})
