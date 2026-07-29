import { describe, expect, it } from "vitest"
import { computeExtensionSubtotal } from "./proration"

const prices = { monthly: 10_00, yearly: 100_00 }

describe("computeExtensionSubtotal", () => {
  it("returns 0 when prices are unavailable", () => {
    expect(computeExtensionSubtotal(undefined, 6)).toBe(0)
  })

  it("bills entirely at the monthly rate under 12 months", () => {
    expect(computeExtensionSubtotal(prices, 6)).toBe(6 * prices.monthly)
  })

  it("bills a single 12-month block at the yearly rate", () => {
    expect(computeExtensionSubtotal(prices, 12)).toBe(prices.yearly)
  })

  it("bills whole years at the yearly rate plus the remainder at monthly", () => {
    // 18 months = one yearly block + 6 months at the monthly rate.
    expect(computeExtensionSubtotal(prices, 18)).toBe(
      prices.yearly + 6 * prices.monthly
    )
  })

  it("bills multiple whole-year blocks at the yearly rate", () => {
    // 24 months (the cap) = two yearly blocks, no remainder.
    expect(computeExtensionSubtotal(prices, 24)).toBe(2 * prices.yearly)
  })

  it("returns 0 for 0 months", () => {
    expect(computeExtensionSubtotal(prices, 0)).toBe(0)
  })
})
