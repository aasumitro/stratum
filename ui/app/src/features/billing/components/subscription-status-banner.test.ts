// @vitest-environment jsdom
//
// Render-level tests for SubscriptionStatusBanner's scheduled-cancellation
// banner: a sub-state of active/trialing/past_due (status stays until the
// cancellation takes effect — only scheduled_cancel_at is set), shown
// alongside an "Undo cancellation" action that fires the callback prop
// passed in from SubscriptionCard (which owns the mutation), not a hook
// here. Also pins that past_due/expired/cancelled without a schedule render
// nothing: billing-layout.tsx's dunning banner is the one banner for those
// states.
import { createElement } from "react"
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import "@/lib/i18n"

import { SubscriptionStatusBanner } from "./subscription-status-banner"
import type { Subscription } from "@/types/billing"

const organizationId = "org-1"

function subscription(overrides: Partial<Subscription> = {}): Subscription {
  return {
    id: "sub-1",
    subject_type: "organization",
    subject_id: organizationId,
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

afterEach(() => {
  cleanup()
})

function renderBanner(
  sub: Subscription,
  overrides: {
    isOwner?: boolean
    onUndoCancellation?: () => void
    undoingCancellation?: boolean
  } = {}
) {
  return render(
    createElement(SubscriptionStatusBanner, {
      sub,
      organizationId,
      isOwner: overrides.isOwner ?? true,
      daysLeft: null,
      undoingCancellation: overrides.undoingCancellation ?? false,
      onUndoCancellation: overrides.onUndoCancellation ?? (() => {}),
    })
  )
}

describe("SubscriptionStatusBanner", () => {
  it("renders nothing extra for a plain active subscription", () => {
    const { container } = renderBanner(subscription())
    expect(container.firstChild).toBeNull()
  })

  it.each(["past_due", "expired", "cancelled"] as const)(
    "renders nothing for %s — the page's dunning banner owns that state",
    (status) => {
      const { container } = renderBanner(subscription({ status }))
      expect(container.firstChild).toBeNull()
    }
  )

  it("shows the scheduled-cancellation banner with the concrete renewal date", () => {
    renderBanner(subscription({ scheduled_cancel_at: "2026-08-01T00:00:00Z" }))
    screen.getByText("Cancellation scheduled")
    screen.getByText(
      "Your subscription remains active until Sep 1, 2026 and won't renew."
    )
  })

  it.each(["trialing", "past_due"] as const)(
    "shows the scheduled-cancellation banner for a %s subscription too",
    (status) => {
      renderBanner(
        subscription({ status, scheduled_cancel_at: "2026-08-01T00:00:00Z" })
      )
      screen.getByText("Cancellation scheduled")
      screen.getByRole("button", { name: "Undo cancellation" })
    }
  )

  it("hides the trial's activate action while a cancellation is scheduled", () => {
    renderBanner(
      subscription({
        status: "trialing",
        scheduled_cancel_at: "2026-08-01T00:00:00Z",
      })
    )
    expect(screen.queryByText("Activate & pay now")).toBeNull()
  })

  it("renders nothing once the cancellation took effect", () => {
    const { container } = renderBanner(
      subscription({
        status: "cancelled",
        scheduled_cancel_at: "2026-08-01T00:00:00Z",
      })
    )
    expect(container.firstChild).toBeNull()
  })

  it("fires onUndoCancellation when the owner clicks Undo cancellation", () => {
    const onUndoCancellation = vi.fn()
    renderBanner(
      subscription({ scheduled_cancel_at: "2026-08-01T00:00:00Z" }),
      {
        onUndoCancellation,
      }
    )

    screen.getByRole("button", { name: "Undo cancellation" }).click()
    expect(onUndoCancellation).toHaveBeenCalledTimes(1)
  })

  it("hides the Undo cancellation action for a non-owner", () => {
    renderBanner(
      subscription({ scheduled_cancel_at: "2026-08-01T00:00:00Z" }),
      { isOwner: false }
    )
    expect(
      screen.queryByRole("button", { name: "Undo cancellation" })
    ).toBeNull()
  })
})
