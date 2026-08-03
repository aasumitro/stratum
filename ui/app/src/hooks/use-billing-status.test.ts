// @vitest-environment jsdom
//
// useBillingStatus pulls in useActiveOrganization(), which reads
// organizationId via TanStack Router's useParams — needs a real router
// context, not just QueryClientProvider. Same minimal-router-harness
// pattern as addons-section.test.ts: one root route plus one child route
// carrying the $organizationId param, backed by createMemoryHistory. The
// hook itself is invoked from that child route's component (a tiny probe
// that hands its result out via a callback) rather than through
// renderHook's own wrapper option, since that option can't place the
// hook-under-test inside a specific route match.
import { createElement } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router"
import { cleanup, render, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

vi.mock("@/lib/api/axios", () => ({
  api: { get: vi.fn() },
}))

import { api } from "@/lib/api/axios"
import { useBillingStatus } from "./use-billing-status"
import type { Invoice } from "@/types/billing"

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

function pendingInvoice(kind: Invoice["kind"]): Invoice {
  return {
    id: "inv-1",
    subscription_id: "sub-1",
    kind,
    status: "pending",
    amount_cents: 1000,
    tax_rate_bps: 0,
    tax_cents: 0,
    switch_to_annual: false,
    currency: "USD",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  }
}

function setupApi(invoices: Invoice[]) {
  vi.mocked(api.get).mockImplementation((url: string) => {
    if (url === "/v1/organizations")
      return Promise.resolve(httpResponse([orgListEntry]))
    if (url === `/v1/organizations/${organizationId}/billing/invoices`)
      return Promise.resolve(httpResponse(invoices))
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

function HookProbe({
  onResult,
}: {
  onResult: (v: ReturnType<typeof useBillingStatus>) => void
}) {
  onResult(useBillingStatus())
  return null
}

function renderInOrg() {
  let captured: ReturnType<typeof useBillingStatus> | undefined
  const rootRoute = createRootRoute()
  const route = createRoute({
    getParentRoute: () => rootRoute,
    path: "/org/$organizationId",
    component: () =>
      createElement(HookProbe, {
        onResult: (v) => {
          captured = v
        },
      }),
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([route]),
    history: createMemoryHistory({
      initialEntries: [`/org/${organizationId}`],
    }),
  })
  render(
    createElement(
      QueryClientProvider,
      { client: queryClient },
      createElement(RouterProvider, { router })
    )
  )
  return () => captured
}

describe("useBillingStatus", () => {
  it("is not blocked with no pending invoices", async () => {
    setupApi([])
    const getResult = renderInOrg()
    await waitFor(() => expect(getResult()?.isBillingBlocked).toBe(false))
  })

  it("blocks on a pending activation invoice", async () => {
    setupApi([pendingInvoice("activation")])
    const getResult = renderInOrg()
    await waitFor(() => expect(getResult()?.hasPendingInvoice).toBe(true))
    expect(getResult()?.isBillingBlocked).toBe(true)
  })

  it("does not block on a pending extension invoice", async () => {
    setupApi([pendingInvoice("extension")])
    const getResult = renderInOrg()
    await waitFor(() => expect(getResult()?.isOwner).toBe(true))
    expect(getResult()?.hasPendingInvoice).toBe(false)
    expect(getResult()?.isBillingBlocked).toBe(false)
  })

  it("does not block on a pending addon_increase invoice", async () => {
    // Regression guard: an unpaid addon-increase invoice pays for capacity
    // additional to the current period, same as an extension pays for time
    // additional to it — neither should lock the owner out of the plan and
    // capacity already paid for and still fully valid.
    setupApi([pendingInvoice("addon_increase")])
    const getResult = renderInOrg()
    await waitFor(() => expect(getResult()?.isOwner).toBe(true))
    expect(getResult()?.hasPendingInvoice).toBe(false)
    expect(getResult()?.isBillingBlocked).toBe(false)
  })
})
