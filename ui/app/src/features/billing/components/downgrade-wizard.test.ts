// @vitest-environment jsdom
//
// Render-level tests for DowngradeWizard's trial-vs-active branch: an
// active (non-trialing) subscription's downgrade is deferred to renewal by
// the backend, so this wizard skips the selection step (nothing is removed
// today — the backend ignores any picks for that status) and shows
// scheduled-effective copy instead of the immediate-change copy; trialing
// keeps today's immediate flow (selection step included, "Downgrade
// complete" copy) exactly as before. Uses createElement instead of JSX so
// this stays a .ts file, matching this repo's other test files. Mocks the
// axios client (the HTTP boundary every hook here ultimately calls
// through), not the hooks themselves.
//
// DowngradeWizard pulls in usePermissions() (via hasPendingInvoice), which
// reads `organizationId` through TanStack Router's useParams — same reason
// addons-section.test.ts needs a real router context, not just
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
import { DowngradeWizard } from "./downgrade-wizard"
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

function setupApi({
  overage = true,
}: {
  overage?: boolean
} = {}) {
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
    if (url === `/v1/organizations/${organizationId}/billing/plans/catalog`)
      return Promise.resolve(httpResponse(plans))
    if (url === `/v1/organizations/${organizationId}/members`)
      return Promise.resolve(httpResponse([]))
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
          ...(overage
            ? {
                overage: {
                  members: {
                    current: 8,
                    allowed: 1,
                    auto_select_removals: ["m1"],
                  },
                },
              }
            : {}),
        })
      )
    return Promise.reject(new Error(`unexpected GET ${url}`))
  })
}

// Several buttons in this wizard stay disabled while their backing
// useInvoicePreview fetch is in flight — waiting for the element to exist
// isn't enough, since a raw DOM .click() on a disabled button is a no-op.
async function clickWhenEnabled(name: string) {
  const button = (await screen.findByRole("button", {
    name,
  })) as HTMLButtonElement
  await waitFor(() => expect(button.disabled).toBe(false))
  button.click()
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

function renderWizard(subscriptionStatus: SubscriptionStatus) {
  const rootRoute = createRootRoute()
  const route = createRoute({
    getParentRoute: () => rootRoute,
    path: "/org/$organizationId",
    component: () =>
      createElement(DowngradeWizard, {
        organizationId,
        open: true,
        onOpenChange: () => {},
        targetPlan: "solo",
        targetCycle: "monthly",
        currentPlan: "growth",
        currentCycle: "monthly",
        currentPeriodEnd: "2026-09-01T00:00:00Z",
        currency: "USD",
        subscriptionStatus,
        onBackToPlans: () => {},
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

describe("DowngradeWizard — active (deferred) subscription", () => {
  it("shows scheduled-effective copy on the first preview screen, not the proration note", async () => {
    setupApi({ overage: false })
    renderWizard("active")

    await screen.findByText("Downgrade to a lower plan")
    screen.getByText(
      "This takes effect at renewal on Sep 1, 2026 — your current plan and its limits stay active until then, and you can undo it anytime before that."
    )
    // The proration note ("new period end ...") only applies to an
    // immediate change and must not render alongside the scheduled-effective
    // copy above, which would contradict it.
    expect(screen.queryByText(/new period end/)).toBeNull()
  })

  it("skips the selection step and shows the scheduled-effective review note", async () => {
    setupApi({ overage: true })
    renderWizard("active")

    await clickWhenEnabled("Continue")

    // Goes straight to Review — the selection step never appears, since
    // nothing is removed today for a deferred downgrade.
    await screen.findByText("Review Downgrade")
    expect(screen.queryByText("Select items to remove")).toBeNull()
    screen.getByText(
      "This takes effect at renewal on Sep 1, 2026 — your current plan and its limits stay active until then, and you can undo it anytime before that."
    )
    expect(screen.queryByText("This can't be undone")).toBeNull()
  })

  it("shows scheduled success copy after confirming", async () => {
    setupApi({ overage: false })
    vi.mocked(api.post).mockResolvedValue(
      httpResponse({
        subscription: {},
        overage: {
          removed_member_auth_subs: [],
          auto_selected_member_subs: [],
          removed_file_ids: [],
          auto_selected_file_ids: [],
        },
      })
    )
    renderWizard("active")

    await clickWhenEnabled("Continue")
    await clickWhenEnabled("Confirm downgrade")

    await screen.findByText("Downgrade scheduled")
    screen.getByText("Your plan will change to Solo at renewal on Sep 1, 2026.")
    expect(screen.queryByText("Your plan has been changed.")).toBeNull()

    expect(api.post).toHaveBeenCalledWith(
      `/v1/organizations/${organizationId}/billing/downgrade`,
      expect.objectContaining({ plan: "solo", cycle: "monthly" }),
      expect.anything()
    )
  })
})

describe("DowngradeWizard — trialing subscription", () => {
  it("still shows the selection step and the immediate-change warning", async () => {
    setupApi({ overage: true })
    renderWizard("trialing")

    await clickWhenEnabled("Continue")

    await screen.findByText("Select items to remove")
    await clickWhenEnabled("Continue")

    await screen.findByText("Review Downgrade")
    await screen.findByText("This can't be undone")
  })

  it("shows the original immediate success copy after confirming", async () => {
    setupApi({ overage: false })
    vi.mocked(api.post).mockResolvedValue(
      httpResponse({
        subscription: {},
        overage: {
          removed_member_auth_subs: [],
          auto_selected_member_subs: [],
          removed_file_ids: [],
          auto_selected_file_ids: [],
        },
      })
    )
    renderWizard("trialing")

    await clickWhenEnabled("Continue")
    await clickWhenEnabled("Confirm downgrade")

    await screen.findByText("Downgrade complete")
    screen.getByText("Your plan has been changed.")
    expect(screen.queryByText("Downgrade scheduled")).toBeNull()
  })
})
