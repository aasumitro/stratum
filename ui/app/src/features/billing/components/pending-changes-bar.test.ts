// @vitest-environment jsdom
//
// Render-level tests for PendingChangesBar's trigger conditions: a renewal
// date inside the near-renewal window shows "At your next renewal", but a
// date already in the past (a lapsed period) is not an upcoming renewal and
// must not be labelled as one. Mocks useInvoicePreview, the component's only
// data boundary. Uses createElement instead of JSX so this stays a .ts file,
// matching this repo's other test files.
import { createElement } from "react"
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import "@/lib/i18n"

import { PendingChangesBar } from "./pending-changes-bar"

vi.mock("@/features/billing/hooks", () => ({
  useInvoicePreview: () => ({
    data: {
      data: {
        plan: "growth",
        cycle: "monthly",
        currency: "USD",
        plan_line_cents: 2900,
        total_cents: 2900,
      },
    },
  }),
}))

const DAY_MS = 86_400_000

afterEach(() => {
  cleanup()
})

function renderBar(nextInvoiceDate: string, hasPendingInvoice = false) {
  return render(
    createElement(PendingChangesBar, {
      organizationId: "org-1",
      nextInvoiceDate,
      isOwner: true,
      hasPendingInvoice,
    })
  )
}

describe("PendingChangesBar", () => {
  it("shows an upcoming renewal inside the window", () => {
    renderBar(new Date(Date.now() + 5 * DAY_MS).toISOString())
    screen.getByText("At your next renewal")
  })

  it("stays hidden when the renewal date is already in the past", () => {
    const { container } = renderBar(
      new Date(Date.now() - 15 * DAY_MS).toISOString()
    )
    expect(container.firstChild).toBeNull()
  })

  it("labels a pending invoice as unpaid, not as a past-dated renewal", () => {
    renderBar(new Date(Date.now() - 15 * DAY_MS).toISOString(), true)
    screen.getByText("You have an unpaid invoice")
    expect(screen.queryByText("At your next renewal")).toBeNull()
  })
})
