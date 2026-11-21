import { describe, expect, it } from "vitest"
import {
  downgradeAppliesNow,
  scheduledChangeDate,
  scheduledChangeLocked,
} from "./scheduled-change"

describe("scheduledChangeDate", () => {
  it("uses the effective date once the renewal has moved period_end on", () => {
    expect(
      scheduledChangeDate({
        period_end: "2026-12-09T00:00:00Z",
        scheduled_effective_at: "2026-11-09T00:00:00Z",
      })
    ).toBe("2026-11-09T00:00:00Z")
  })
  it("falls back to period_end", () => {
    expect(scheduledChangeDate({ period_end: "2026-12-09T00:00:00Z" })).toBe(
      "2026-12-09T00:00:00Z"
    )
  })
})

describe("scheduledChangeLocked", () => {
  it("is locked when the priced renewal moved period_end past the effective date", () => {
    expect(
      scheduledChangeLocked({
        period_end: "2026-12-09T00:00:00Z",
        scheduled_effective_at: "2026-11-09T00:00:00Z",
      })
    ).toBe(true)
  })
  it("is open while the renewal is unpaid", () => {
    expect(
      scheduledChangeLocked({
        period_end: "2026-11-09T00:00:00Z",
        scheduled_effective_at: "2026-11-09T00:00:00Z",
      })
    ).toBe(false)
    expect(scheduledChangeLocked({ period_end: "2026-11-09T00:00:00Z" })).toBe(
      false
    )
  })
})

describe("downgradeAppliesNow", () => {
  it("applies at once on a trial and on an ended subscription", () => {
    expect(downgradeAppliesNow("trialing")).toBe(true)
    expect(downgradeAppliesNow("expired")).toBe(true)
    expect(downgradeAppliesNow("cancelled")).toBe(true)
  })
  it("waits for the period end on a paid one", () => {
    expect(downgradeAppliesNow("active")).toBe(false)
    expect(downgradeAppliesNow("past_due")).toBe(false)
  })
})
