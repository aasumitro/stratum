// @vitest-environment jsdom
//
// Render-level tests for SubscriptionDetailsGrid's scheduled-downgrade
// badge: shown (with a working Undo) once scheduled_plan is set, and gated
// behind isOwner the same way every other Undo action in this feature is.
// Uses createElement instead of JSX so this stays a .ts file, matching this
// repo's other test files. Mocks the axios client (the HTTP boundary
// useUndoScheduledDowngrade ultimately calls through), not the hook itself.
import { createElement } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { cleanup, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import "@/lib/i18n"

vi.mock("@/lib/api/axios", () => ({
  api: { get: vi.fn(), post: vi.fn() },
}))

import { api } from "@/lib/api/axios"
import { SubscriptionDetailsGrid } from "./subscription-details-grid"
import type { Subscription } from "@/types/billing"

const organizationId = "org-1"

function httpResponse<T>(data: T) {
  return {
    data: {
      data,
      status: { request_id: "req-1", error: false, message: "" },
      pagination: {
        limit: 0,
        offset: 0,
        current_page: 0,
        total_pages: 0,
        total_items: 0,
      },
    },
  }
}

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

let queryClient: QueryClient

beforeEach(() => {
  queryClient = new QueryClient()
  vi.mocked(api.get).mockReset()
  vi.mocked(api.post).mockReset()
})

afterEach(() => {
  cleanup()
})

function renderGrid(sub: Subscription, isOwner = true) {
  return render(
    createElement(
      QueryClientProvider,
      { client: queryClient },
      createElement(SubscriptionDetailsGrid, {
        sub,
        daysLeft: null,
        organizationId,
        isOwner,
      })
    )
  )
}

describe("SubscriptionDetailsGrid", () => {
  it("shows no scheduled badge when nothing is scheduled", () => {
    renderGrid(subscription())
    expect(screen.queryByText(/at renewal/)).toBeNull()
  })

  it("shows the scheduled-plan badge with the concrete renewal date", () => {
    renderGrid(subscription({ scheduled_plan: "solo" }))
    screen.getByText("Scheduled: solo at renewal on Sep 1, 2026")
  })

  it("shows the Undo action for an owner and fires the undo mutation", async () => {
    vi.mocked(api.post).mockResolvedValue(httpResponse(subscription()))
    renderGrid(subscription({ scheduled_plan: "solo" }), true)

    const undoButton = screen.getByRole("button", { name: "Undo" })
    undoButton.click()

    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        `/v1/organizations/${organizationId}/billing/downgrade/undo`,
        undefined,
        expect.anything()
      )
    )
  })

  it("hides the Undo action for a non-owner", () => {
    renderGrid(subscription({ scheduled_plan: "solo" }), false)
    expect(screen.queryByRole("button", { name: "Undo" })).toBeNull()
  })
})
