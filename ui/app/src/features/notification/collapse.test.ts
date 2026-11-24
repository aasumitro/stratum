import { describe, expect, it } from "vitest"
import type { Notification } from "@/types/notification"
import { chunk, collapseRuns, isRunOpen, toggleRun } from "./collapse"

function n(id: string, channel: string, org: string, hoursAgo: number) {
  return {
    id,
    channel,
    organization_id: org,
    created_at: new Date(
      Date.UTC(2026, 10, 24, 12) - hoursAgo * 3_600_000
    ).toISOString(),
  } as Notification
}

describe("collapseRuns", () => {
  it("folds neighbours of the same kind and organization within a day", () => {
    const runs = collapseRuns([
      n("a", "invoice_created", "o1", 0),
      n("b", "invoice_created", "o1", 3),
      n("c", "invoice_created", "o1", 30),
      n("d", "member_joined", "o1", 31),
      n("e", "member_joined", "o2", 32),
    ])
    expect(runs.map((r) => [r.lead.id, r.rest.map((x) => x.id)])).toEqual([
      ["a", ["b"]],
      ["c", []],
      ["d", []],
      ["e", []],
    ])
  })

  it("never joins items that are not next to each other", () => {
    const runs = collapseRuns([
      n("a", "invoice_created", "o1", 0),
      n("b", "member_joined", "o1", 1),
      n("c", "invoice_created", "o1", 2),
    ])
    expect(runs).toHaveLength(3)
  })

  it("keeps a run open when items join it at either end", () => {
    const before = collapseRuns([n("b", "x", "o1", 1), n("c", "x", "o1", 2)])
    const open = toggleRun(before[0], new Set())
    const newer = collapseRuns([
      n("a", "x", "o1", 0),
      n("b", "x", "o1", 1),
      n("c", "x", "o1", 2),
    ])
    const older = collapseRuns([
      n("b", "x", "o1", 1),
      n("c", "x", "o1", 2),
      n("d", "x", "o1", 3),
    ])
    expect(isRunOpen(newer[0], open)).toBe(true)
    expect(isRunOpen(older[0], open)).toBe(true)
    expect(isRunOpen(newer[0], toggleRun(newer[0], open))).toBe(false)
  })

  it("chunks ids for requests of at most 100", () => {
    const ids = Array.from({ length: 250 }, (_, i) => String(i))
    expect(chunk(ids, 100).map((c) => c.length)).toEqual([100, 100, 50])
  })
})
