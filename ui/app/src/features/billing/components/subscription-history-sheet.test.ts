// @vitest-environment jsdom
//
// Render-level tests for SubscriptionHistorySheet's rendering branches: the
// phase badge per state, the cycle-only-switch copy, and an addon_change
// entry resolving its addon name from the org-scoped catalog.
// Mocks the axios client, not the hooks, matching this repo's other
// component tests (e.g. addons-section.test.ts).
import { createElement } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import "@/lib/i18n"

vi.mock("@/lib/api/axios", () => ({
  api: { get: vi.fn() },
}))

import { api } from "@/lib/api/axios"
import { SubscriptionHistorySheet } from "./subscription-history-sheet"
import type { SubscriptionHistory } from "@/types/billing"
import type { Addon } from "@/types/reference"

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

const catalog: Addon[] = [
  {
    id: "extra-seat",
    name: "Extra Seat",
    description: "1 additional member",
    prices: { USD: { monthly: 100, yearly: 1000 } },
    features: { members: 1 },
    active: true,
    created_at: "2026-01-01T00:00:00Z",
  },
]

function baseHistoryRow(
  overrides: Partial<SubscriptionHistory> = {}
): SubscriptionHistory {
  return {
    id: "h1",
    subscription_id: "sub-1",
    action: "downgrade",
    from_plan: "growth",
    to_plan: "solo",
    amount_cents: 0,
    currency: "USD",
    changed_by: "u1",
    changed_by_name: "Jane",
    changed_by_kind: "user",
    changed_at: "2026-08-01T00:00:00Z",
    ...overrides,
  }
}

function setupApi(history: SubscriptionHistory[]) {
  vi.mocked(api.get).mockImplementation((url: string) => {
    if (url === `/v1/organizations/${organizationId}/billing/history`)
      return Promise.resolve(httpResponse(history))
    if (url === `/v1/organizations/${organizationId}/billing/addons/catalog`)
      return Promise.resolve(httpResponse(catalog))
    return Promise.reject(new Error(`unexpected GET ${url}`))
  })
}

let queryClient: QueryClient

beforeEach(() => {
  queryClient = new QueryClient()
  vi.mocked(api.get).mockReset()
})

afterEach(() => {
  cleanup()
})

function renderSheet() {
  return render(
    createElement(
      QueryClientProvider,
      { client: queryClient },
      createElement(SubscriptionHistorySheet, {
        organizationId,
        open: true,
        onOpenChange: () => {},
      })
    )
  )
}

describe("SubscriptionHistorySheet", () => {
  it("renders a Scheduled phase badge for a scheduled row", async () => {
    setupApi([
      baseHistoryRow({
        phase: "scheduled",
        effective_at: "2026-09-01T00:00:00Z",
      }),
    ])
    renderSheet()

    await screen.findByText("Scheduled")
    await screen.findByText("Scheduled for Sep 1, 2026")
  })

  it("renders an Undone phase badge with no effective-date line", async () => {
    setupApi([baseHistoryRow({ phase: "undone" })])
    renderSheet()

    await screen.findByText("Undone")
    expect(screen.queryByText(/Scheduled for|Applied /)).toBeNull()
  })

  it("renders a cycle-only switch as its own line, not a plan no-op", async () => {
    setupApi([
      baseHistoryRow({
        from_plan: "growth",
        to_plan: "growth",
        from_cycle: "yearly",
        to_cycle: "monthly",
      }),
    ])
    renderSheet()

    await screen.findByText("Switched billing cycle: Yearly → Monthly")
    expect(screen.queryByText(/Changed the plan/)).toBeNull()
  })

  it("resolves an addon_change entry's addon name from the catalog", async () => {
    setupApi([
      baseHistoryRow({
        action: "addon_change",
        from_plan: undefined,
        to_plan: undefined,
        metadata: { addon_id: "extra-seat", from_quantity: 2, to_quantity: 5 },
      }),
    ])
    renderSheet()

    await screen.findByText("Extra Seat: 2 → 5")
  })
})
