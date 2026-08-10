// @vitest-environment jsdom
//
// Render-level test for PlanSelector's own inline preview note — the picker
// screen shown before either wizard opens. A downgrade pick on a
// non-trialing subscription must show scheduled-effective copy, not the
// pure-proration InvoicePreviewNote (which would contradict the
// scheduled-effective reality of that change). Mocks the axios client, not
// the hooks, matching this repo's other component tests (e.g.
// addons-section.test.ts).
import { createElement } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router"
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import "@/lib/i18n"

vi.mock("@/lib/api/axios", () => ({
  api: { get: vi.fn() },
}))

import { api } from "@/lib/api/axios"
import { PlanSelector } from "./plan-selector"
import type { Plan } from "@/types/reference"

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

const orgListEntry = {
  id: organizationId,
  slug: "acme",
  name: "Acme",
  status: "active" as const,
  owner_id: "u1",
  role: "owner" as const,
  joined_at: "2026-01-01T00:00:00Z",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
}

const plans: Plan[] = [
  {
    id: "growth",
    name: "Growth",
    description: "",
    prices: { USD: { monthly: 5000, yearly: 50000 } },
    limits: { members: 20 },
    features: [],
    sort_order: 2,
    config_values: {},
    active: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
  {
    id: "solo",
    name: "Solo",
    description: "",
    prices: { USD: { monthly: 1000, yearly: 10000 } },
    limits: { members: 1 },
    features: [],
    sort_order: 1,
    config_values: {},
    active: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
]

function setupApi() {
  vi.mocked(api.get).mockImplementation((url: string) => {
    if (url === "/v1/organizations")
      return Promise.resolve(httpResponse([orgListEntry]))
    if (url === `/v1/organizations/${organizationId}/billing/invoices`)
      return Promise.resolve(httpResponse([]))
    if (url === `/v1/organizations/${organizationId}`)
      return Promise.resolve(
        httpResponse({
          id: organizationId,
          slug: "acme",
          name: "Acme",
          status: "active",
          owner_id: "u1",
          invite_code_enabled: false,
          timezone: "UTC",
          locale: "en",
          country_code: "US",
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        })
      )
    if (url === `/v1/organizations/${organizationId}/billing/plans/catalog`)
      return Promise.resolve(httpResponse(plans))
    if (
      url ===
      `/v1/organizations/${organizationId}/billing/preview?plan=solo&cycle=monthly`
    )
      return Promise.resolve(
        httpResponse({
          plan: "solo",
          cycle: "monthly",
          currency: "USD",
          plan_line_cents: 1000,
          total_cents: 1000,
          new_period_end: "2026-09-01T00:00:00Z",
        })
      )
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

function renderSelector() {
  const rootRoute = createRootRoute()
  const route = createRoute({
    getParentRoute: () => rootRoute,
    path: "/org/$organizationId",
    component: () =>
      createElement(PlanSelector, {
        organizationId,
        currentPlan: "growth",
        currentCycle: "monthly",
        currentPeriodEnd: "2026-09-01T00:00:00Z",
        currency: "USD",
        subscriptionStatus: "active",
        open: true,
        onOpenChange: () => {},
      }),
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([route]),
    history: createMemoryHistory({
      initialEntries: [`/org/${organizationId}`],
    }),
  })
  return render(
    createElement(
      QueryClientProvider,
      { client: queryClient },
      createElement(RouterProvider, { router })
    )
  )
}

describe("PlanSelector", () => {
  it("shows scheduled-effective copy for a downgrade pick on a non-trialing subscription, not the proration note", async () => {
    setupApi()
    renderSelector()

    const soloOption = await screen.findByText("Solo")
    soloOption.click()

    await screen.findByText(
      "This takes effect at renewal on Sep 1, 2026 — your current plan and its limits stay active until then, and you can undo it anytime before that."
    )
    expect(screen.queryByText(/new period end/)).toBeNull()
  })
})
