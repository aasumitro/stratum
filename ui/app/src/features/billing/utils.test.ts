import { describe, expect, it, vi } from "vitest"
import {
  computeAmendmentDiff,
  applyAmendmentDiff,
  type AmendmentDiff,
} from "./utils"
import type { AttachedAddon } from "@/types/billing"

function makeAttached(overrides: Partial<AttachedAddon> = {}): AttachedAddon {
  return {
    addon_id: "extra-seat",
    name: "Extra Seat",
    quantity: 3,
    prices: {},
    ...overrides,
  }
}

describe("computeAmendmentDiff", () => {
  it("buckets a brand-new attach as immediate", () => {
    const diff = computeAmendmentDiff({ "extra-seat": 2 }, [])
    expect(diff).toEqual({
      immediate: [{ addonId: "extra-seat", fromQty: 0, toQty: 2 }],
      scheduled: [],
    })
  })

  it("buckets a quantity increase as immediate", () => {
    const diff = computeAmendmentDiff({ "extra-seat": 5 }, [
      makeAttached({ quantity: 3 }),
    ])
    expect(diff).toEqual({
      immediate: [{ addonId: "extra-seat", fromQty: 3, toQty: 5 }],
      scheduled: [],
    })
  })

  it("buckets a quantity decrease as scheduled", () => {
    const diff = computeAmendmentDiff({ "extra-seat": 1 }, [
      makeAttached({ quantity: 3 }),
    ])
    expect(diff).toEqual({
      immediate: [],
      scheduled: [{ addonId: "extra-seat", fromQty: 3, toQty: 1 }],
    })
  })

  it("treats an addon dropped from staged as a scheduled removal to 0", () => {
    const diff = computeAmendmentDiff({}, [makeAttached({ quantity: 3 })])
    expect(diff).toEqual({
      immediate: [],
      scheduled: [{ addonId: "extra-seat", fromQty: 3, toQty: 0 }],
    })
  })

  it("excludes addons whose staged quantity is unchanged", () => {
    const diff = computeAmendmentDiff({ "extra-seat": 3 }, [
      makeAttached({ quantity: 3 }),
    ])
    expect(diff).toEqual({ immediate: [], scheduled: [] })
  })

  it("splits a mixed selection into both buckets", () => {
    const diff = computeAmendmentDiff(
      { "extra-seat": 5, "extra-workspace": 1 },
      [
        makeAttached({ addon_id: "extra-seat", quantity: 3 }),
        makeAttached({
          addon_id: "extra-workspace",
          name: "Extra Workspace",
          quantity: 4,
        }),
      ]
    )
    expect(diff.immediate).toEqual([
      { addonId: "extra-seat", fromQty: 3, toQty: 5 },
    ])
    expect(diff.scheduled).toEqual([
      { addonId: "extra-workspace", fromQty: 4, toQty: 1 },
    ])
  })
})

describe("applyAmendmentDiff", () => {
  it("calls attach for every immediate change", () => {
    const attach = vi.fn()
    const detach = vi.fn()
    const diff: AmendmentDiff = {
      immediate: [{ addonId: "extra-seat", fromQty: 0, toQty: 2 }],
      scheduled: [],
    }
    applyAmendmentDiff(diff, attach, detach)
    expect(attach).toHaveBeenCalledWith({ addon_id: "extra-seat", quantity: 2 })
    expect(detach).not.toHaveBeenCalled()
  })

  it("calls detach for a scheduled change down to 0", () => {
    const attach = vi.fn()
    const detach = vi.fn()
    const diff: AmendmentDiff = {
      immediate: [],
      scheduled: [{ addonId: "extra-seat", fromQty: 3, toQty: 0 }],
    }
    applyAmendmentDiff(diff, attach, detach)
    expect(detach).toHaveBeenCalledWith("extra-seat")
    expect(attach).not.toHaveBeenCalled()
  })

  it("calls attach (not detach) for a scheduled non-zero decrease", () => {
    const attach = vi.fn()
    const detach = vi.fn()
    const diff: AmendmentDiff = {
      immediate: [],
      scheduled: [{ addonId: "extra-seat", fromQty: 3, toQty: 1 }],
    }
    applyAmendmentDiff(diff, attach, detach)
    expect(attach).toHaveBeenCalledWith({ addon_id: "extra-seat", quantity: 1 })
    expect(detach).not.toHaveBeenCalled()
  })
})
