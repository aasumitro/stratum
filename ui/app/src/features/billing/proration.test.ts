import { describe, expect, it } from "vitest"
import {
  computeExtensionAddonSubtotal,
  computeExtensionSubtotal,
} from "./proration"

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

describe("computeExtensionAddonSubtotal", () => {
  const seats = {
    quantity: 3,
    prices: { USD: { monthly: 5_00, yearly: 50_00 } },
  }
  const storage = {
    quantity: 1,
    prices: { USD: { monthly: 2_00, yearly: 20_00 } },
  }
  const eurOnly = {
    quantity: 5,
    prices: { EUR: { monthly: 1_00, yearly: 10_00 } },
  }

  it("returns 0 when there are no attached addons", () => {
    expect(computeExtensionAddonSubtotal([], "USD", 13)).toBe(0)
  })

  it("bills entirely at the monthly rate under 12 months, times quantity", () => {
    expect(computeExtensionAddonSubtotal([seats], "USD", 6)).toBe(
      6 * seats.prices.USD.monthly * seats.quantity
    )
  })

  it("bills a single 12-month block at the yearly rate, times quantity", () => {
    expect(computeExtensionAddonSubtotal([seats], "USD", 12)).toBe(
      seats.prices.USD.yearly * seats.quantity
    )
  })

  it("bills whole years at the yearly rate plus the remainder at monthly, times quantity", () => {
    expect(computeExtensionAddonSubtotal([seats], "USD", 13)).toBe(
      (seats.prices.USD.yearly + seats.prices.USD.monthly) * seats.quantity
    )
  })

  it("sums multiple addons independently", () => {
    expect(computeExtensionAddonSubtotal([seats, storage], "USD", 13)).toBe(
      (seats.prices.USD.yearly + seats.prices.USD.monthly) * seats.quantity +
        (storage.prices.USD.yearly + storage.prices.USD.monthly) *
          storage.quantity
    )
  })

  it("contributes 0 for an addon missing a price entry for the requested currency", () => {
    expect(computeExtensionAddonSubtotal([eurOnly], "USD", 13)).toBe(0)
  })
})
