// Pure tests for computeDunningBanners' ended-subscription split: an expired
// or cancelled subscription with an open subscription/activation invoice
// gets the "pay the open invoice" banner (billing-layout.tsx attaches the
// Pay → anchor to its id); without one there is nothing to pay yet, so it
// gets the "resume" banner instead (billing-layout.tsx attaches the owner's
// Resume button). `t` echoes the key, so assertions name the i18n keys
// directly.
import { describe, expect, it } from "vitest"

import { computeDunningBanners } from "./dunning-banners"
import type { Subscription } from "@/types/billing"

const t = (key: string) => key

function subscription(overrides: Partial<Subscription> = {}): Subscription {
  return {
    id: "sub-1",
    subject_type: "organization",
    subject_id: "org-1",
    plan: "growth",
    status: "active",
    cycle: "monthly",
    currency: "USD",
    max_extendable_months: 24,
    period_end: "2026-09-01T00:00:00Z",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  }
}

describe("computeDunningBanners", () => {
  it("points an expired subscription with an open invoice at paying it", () => {
    expect(
      computeDunningBanners(subscription({ status: "expired" }), [], true, t)
    ).toEqual([
      {
        id: "dunning-expired",
        severity: "critical",
        message: "billing.dunning.expired",
      },
    ])
  })

  it("asks to resume an expired subscription with no open invoice", () => {
    expect(
      computeDunningBanners(subscription({ status: "expired" }), [], false, t)
    ).toEqual([
      {
        id: "dunning-expired-resume",
        severity: "critical",
        message: "billing.dunning.expiredResume",
      },
    ])
  })

  it.each([
    [true, "dunning-expired", "billing.dunning.expired"],
    [false, "dunning-expired-resume", "billing.dunning.expiredResume"],
  ] as const)(
    "gives a cancelled subscription the expired banners (open invoice: %s)",
    (hasPendingInvoice, id, message) => {
      expect(
        computeDunningBanners(
          subscription({ status: "cancelled" }),
          [],
          hasPendingInvoice,
          t
        )
      ).toEqual([{ id, severity: "critical", message }])
    }
  )

  it("shows the past-due banner while a period-backing invoice is open", () => {
    const banners = computeDunningBanners(
      subscription({ status: "past_due" }),
      [],
      true,
      t
    )
    expect(banners.map((b) => b.id)).toEqual(["dunning-past-due"])
  })

  it("shows no past-due banner once nothing is left to pay", () => {
    // A scheduled cancellation voids the renewal invoice; the
    // scheduled-cancel banner covers that state instead.
    expect(
      computeDunningBanners(
        subscription({
          status: "past_due",
          scheduled_cancel_at: "2026-08-01T00:00:00Z",
        }),
        [],
        false,
        t
      )
    ).toEqual([])
  })

  it("shows nothing for a plain active subscription", () => {
    expect(computeDunningBanners(subscription(), [], false, t)).toEqual([])
  })
})
