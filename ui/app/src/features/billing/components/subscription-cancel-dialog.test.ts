// @vitest-environment jsdom
//
// Render-level tests for SubscriptionCancelDialog's trial-vs-active branch:
// on an active subscription cancellation defers to renewal (status stays
// "active"), so the success step must offer "Undo cancellation" instead of
// "Resume subscription" and state that access continues rather than "your
// subscription has been cancelled"; trialing cancels immediately and keeps
// today's Resume-based success step exactly as before. Uses createElement
// instead of JSX so this stays a .ts file, matching this repo's other test
// files. Mocks the axios client (the HTTP boundary every hook here
// ultimately calls through), not the hooks themselves.
//
// SubscriptionCancelDialog pulls in usePermissions() (via hasPendingInvoice),
// which reads `organizationId` through TanStack Router's useParams — same
// reason addons-section.test.ts needs a real router context, not just
// QueryClientProvider.
import { createElement } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router"
import { cleanup, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import "@/lib/i18n"

vi.mock("@/lib/api/axios", () => ({
  api: { get: vi.fn(), post: vi.fn() },
}))

import { api } from "@/lib/api/axios"
import { SubscriptionCancelDialog } from "./subscription-cancel-dialog"
import type { SubscriptionStatus } from "@/types/billing"
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
]

function setupApi() {
  vi.mocked(api.get).mockImplementation((url: string) => {
    if (url === "/v1/organizations")
      return Promise.resolve(httpResponse([orgListEntry]))
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
    if (url === "/v1/references/plans?country_code=US")
      return Promise.resolve(httpResponse(plans))
    return Promise.reject(new Error(`unexpected GET ${url}`))
  })
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

function renderDialog(status: SubscriptionStatus) {
  const rootRoute = createRootRoute()
  const route = createRoute({
    getParentRoute: () => rootRoute,
    path: "/org/$organizationId",
    component: () =>
      createElement(SubscriptionCancelDialog, {
        organizationId,
        plan: "growth",
        cycle: "monthly",
        currency: "USD",
        periodEnd: "2026-09-01T00:00:00Z",
        status,
        canCancel: true,
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

async function goThroughToConfirm() {
  ;(await screen.findByRole("button", { name: "Cancel subscription" })).click()
  ;(await screen.findByRole("radio", { name: "No longer need it" })).click()
  ;(await screen.findByRole("button", { name: "Continue" })).click()
  ;(await screen.findByRole("button", { name: "Confirm cancellation" })).click()
}

describe("SubscriptionCancelDialog — active (deferred) subscription", () => {
  it("shows scheduled-cancellation success copy and an Undo cancellation action", async () => {
    setupApi()
    vi.mocked(api.post).mockResolvedValue(httpResponse({}))
    renderDialog("active")

    await goThroughToConfirm()

    await screen.findByText("Cancellation scheduled")
    screen.getByText(
      "Your subscription remains active until Sep 1, 2026 and will not renew. You can undo this anytime before then."
    )
    expect(screen.queryByText("Subscription cancelled")).toBeNull()

    screen.getByRole("button", { name: "Undo cancellation" }).click()

    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        `/v1/organizations/${organizationId}/billing/cancel/undo`,
        undefined,
        expect.anything()
      )
    )
  })
})

describe("SubscriptionCancelDialog — trialing subscription", () => {
  it("keeps the original immediate-cancel success copy and a Resume action", async () => {
    setupApi()
    vi.mocked(api.post).mockResolvedValue(httpResponse({}))
    renderDialog("trialing")

    await goThroughToConfirm()

    await screen.findByText("Subscription cancelled")
    expect(screen.queryByText("Cancellation scheduled")).toBeNull()

    screen.getByRole("button", { name: "Resume subscription" }).click()

    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        `/v1/organizations/${organizationId}/billing/resume`,
        undefined,
        expect.anything()
      )
    )
  })
})
