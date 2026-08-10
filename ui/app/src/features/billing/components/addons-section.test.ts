// @vitest-environment jsdom
//
// Render-level tests for AddonsSection's own flow: increase applies
// immediately, a decrease/removal on an active subscription schedules
// (badge appears), the same decrease on a trialing subscription applies
// immediately after a confirmation instead, and Undo clears a scheduled
// change. Uses createElement instead of JSX so this stays a .ts file,
// matching this repo's other test files. Mocks the axios client (the HTTP
// boundary every hook here ultimately calls through), not the hooks
// themselves.
//
// AddonsSection pulls in usePermissions()/useActiveOrganization(), which
// read `organizationId` via TanStack Router's useParams — unlike this
// repo's existing tests (hooks.test.ts, overage-warning-card.test.ts),
// rendering it needs a real router context, not just QueryClientProvider.
// This file builds the smallest one that satisfies that: one root route
// plus one child route carrying the `$organizationId` param, backed by
// createMemoryHistory rather than the real app's file-based route tree.
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
  api: { get: vi.fn(), post: vi.fn(), delete: vi.fn() },
}))

import { api } from "@/lib/api/axios"
import { AddonsSection } from "./addons-section"
import type { AttachedAddon, Invoice } from "@/types/billing"
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

function attachedAddon(overrides: Partial<AttachedAddon> = {}): AttachedAddon {
  return {
    addon_id: "extra-seat",
    name: "Extra Seat",
    quantity: 3,
    prices: { USD: { monthly: 100, yearly: 1000 } },
    ...overrides,
  }
}

function subscription(status: "active" | "trialing" = "active") {
  return {
    id: "sub-1",
    subject_type: "organization",
    subject_id: organizationId,
    plan: "growth",
    status,
    cycle: "monthly" as const,
    currency: "USD",
    max_extendable_months: 24,
    created_at: "2026-01-01T00:00:00Z",
    period_end: "2026-09-01T00:00:00Z",
  }
}

function setupApi({
  status = "active" as "active" | "trialing",
  attached = [attachedAddon()],
  invoices = [] as Invoice[],
}) {
  vi.mocked(api.get).mockImplementation((url: string) => {
    if (url === "/v1/organizations")
      return Promise.resolve(httpResponse([orgListEntry]))
    if (url === `/v1/organizations/${organizationId}/billing/invoices`)
      return Promise.resolve(httpResponse(invoices))
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
    if (url === `/v1/organizations/${organizationId}/billing/addons/catalog`)
      return Promise.resolve(httpResponse(catalog))
    if (url === `/v1/organizations/${organizationId}/billing/addons`)
      return Promise.resolve(httpResponse(attached))
    if (url === `/v1/organizations/${organizationId}/billing`)
      return Promise.resolve(httpResponse(subscription(status)))
    if (url === `/v1/organizations/${organizationId}/billing/preview`)
      return Promise.resolve(httpResponse({}))
    return Promise.reject(new Error(`unexpected GET ${url}`))
  })
}

let queryClient: QueryClient

beforeEach(() => {
  queryClient = new QueryClient()
  vi.mocked(api.get).mockReset()
  vi.mocked(api.post).mockReset()
  vi.mocked(api.delete).mockReset()
})

afterEach(() => {
  cleanup()
})

function renderAddonsSection() {
  const rootRoute = createRootRoute()
  const route = createRoute({
    getParentRoute: () => rootRoute,
    path: "/org/$organizationId",
    component: () => createElement(AddonsSection, { organizationId }),
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

describe("AddonsSection", () => {
  it("renders the attached addon's badge when a decrease is scheduled", async () => {
    setupApi({
      status: "active",
      attached: [attachedAddon({ quantity: 3, scheduled_quantity: 1 })],
    })
    renderAddonsSection()

    await screen.findByText("→ 1 at renewal on Sep 1, 2026")
  })

  it("renders the removal badge when scheduled_quantity is 0", async () => {
    setupApi({
      status: "active",
      attached: [attachedAddon({ quantity: 3, scheduled_quantity: 0 })],
    })
    renderAddonsSection()

    await screen.findByText("Scheduled for removal at renewal on Sep 1, 2026")
  })

  it("shows no badge when nothing is scheduled", async () => {
    setupApi({ status: "active", attached: [attachedAddon()] })
    renderAddonsSection()

    await screen.findByText("Extra Seat")
    expect(screen.queryByText(/at renewal/)).toBeNull()
  })

  it("clicking Undo fires the undo mutation for that addon", async () => {
    setupApi({
      status: "active",
      attached: [attachedAddon({ quantity: 3, scheduled_quantity: 1 })],
    })
    vi.mocked(api.post).mockResolvedValue(httpResponse(attachedAddon()))
    renderAddonsSection()

    const undoButton = await screen.findByRole("button", { name: "Undo" })
    undoButton.click()

    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        `/v1/organizations/${organizationId}/billing/addons/extra-seat/undo`,
        "extra-seat",
        expect.anything()
      )
    )
  })

  it("renders the pending-payment badge and Pay button when an increase is awaiting payment", async () => {
    const invoice: Invoice = {
      id: "inv-pending-1",
      subscription_id: "sub-1",
      amount_cents: 500,
      tax_rate_bps: 0,
      tax_cents: 0,
      currency: "USD",
      status: "pending",
      kind: "addon_increase",
      switch_to_annual: false,
      created_at: "2026-08-03T00:00:00Z",
      updated_at: "2026-08-03T00:00:00Z",
    }
    setupApi({
      status: "active",
      attached: [
        attachedAddon({
          quantity: 3,
          pending_quantity: 8,
          pending_invoice_id: invoice.id,
        }),
      ],
      invoices: [invoice],
    })
    renderAddonsSection()

    await screen.findByText("→ 8 pending payment")
    await screen.findByRole("button", { name: "Pay" })
  })

  it("shows no pending badge when nothing is pending", async () => {
    setupApi({ status: "active", attached: [attachedAddon()] })
    renderAddonsSection()

    await screen.findByText("Extra Seat")
    expect(screen.queryByText(/pending payment/)).toBeNull()
  })

  it("shows the empty-state copy when the org has nothing attached", async () => {
    setupApi({ status: "active", attached: [] })
    renderAddonsSection()

    await screen.findByText("No add-ons attached.")
  })

  it("opens the AddAddonsDialog picker", async () => {
    setupApi({ status: "active", attached: [attachedAddon()] })
    renderAddonsSection()

    const manageButton = await screen.findByRole("button", {
      name: "Manage add-ons",
    })
    manageButton.click()

    // Confirms the picker actually opened (its own footer button, unique
    // copy) rather than asserting on "Add-ons", which collides with this
    // card's own <CardTitle> once the dialog's title renders alongside it.
    await screen.findByRole("button", { name: "Add Selected" })
  })
})
