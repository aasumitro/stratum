// @vitest-environment jsdom
//
// Render-level tests for SubscriptionCancelDialog: a cancel is scheduled for
// the end of the paid period (or trial) on every status, so the success step
// states that access continues and offers "Undo cancellation"; the review
// step warns about an unpaid invoice only when it stays owed after the
// cancel (an activation), not for a renewal the cancel voids. Uses
// createElement instead of JSX so this stays a .ts file, matching this
// repo's other test files. Mocks the axios client (the HTTP boundary every
// hook here ultimately calls through), not the hooks themselves.
//
// SubscriptionCancelDialog pulls in usePermissions() (via hasBlockingInvoice),
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

// The offers' wizards have their own tests; here they only need Back and Close.
vi.mock("./downgrade-wizard", async () => {
  const { createElement: h } = await import("react")
  return {
    DowngradeWizard: (p: {
      onBackToPlans: () => void
      onOpenChange: (v: boolean) => void
    }) =>
      h(
        "div",
        null,
        h("button", { onClick: p.onBackToPlans }, "Wizard back"),
        h("button", { onClick: () => p.onOpenChange(false) }, "Wizard close")
      ),
  }
})

import { api } from "@/lib/api/axios"
import { queryKeys } from "@/lib/api/keys"
import { SubscriptionCancelDialog } from "./subscription-cancel-dialog"
import type { Invoice } from "@/types/billing"
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
    id: "starter",
    name: "Starter",
    description: "",
    prices: { USD: { monthly: 2000, yearly: 24000 } },
    limits: { members: 5 },
    features: [],
    sort_order: 1,
    config_values: {},
    active: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
]

function pendingInvoice(kind: Invoice["kind"]): Invoice {
  return {
    id: "inv-1",
    subscription_id: "sub-1",
    kind,
    status: "pending",
    amount_cents: 5000,
    tax_rate_bps: 0,
    tax_cents: 0,
    switch_to_annual: false,
    currency: "USD",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  }
}

function setupApi(invoices: Invoice[] = []) {
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
    if (url === `/v1/organizations/${organizationId}/billing/invoices`)
      return Promise.resolve(httpResponse(invoices))
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

function renderDialog(offersAllowed = true) {
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
        canCancel: true,
        subscriptionStatus: "active",
        offersAllowed,
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

describe("SubscriptionCancelDialog", () => {
  it("offers the lower plan and yearly billing, with their savings, for too expensive", async () => {
    setupApi()
    renderDialog()
    ;(
      await screen.findByRole("button", { name: "Cancel subscription" })
    ).click()
    ;(await screen.findByRole("radio", { name: "It's too expensive" })).click()

    await screen.findByText(
      "Switch to Starter: $20/mo instead of $50/mo (save $30/mo)."
    )
    screen.getByRole("button", { name: "Downgrade instead" })
    screen.getByRole("button", { name: "Switch to yearly" })
  })

  it("returns from an offer's wizard to the reasons, with the choice kept", async () => {
    setupApi()
    renderDialog()
    ;(
      await screen.findByRole("button", { name: "Cancel subscription" })
    ).click()
    ;(await screen.findByRole("radio", { name: "It's too expensive" })).click()
    ;(await screen.findByRole("button", { name: "Downgrade instead" })).click()
    ;(await screen.findByRole("button", { name: "Wizard back" })).click()

    const radio = await screen.findByRole("radio", {
      name: "It's too expensive",
    })
    expect(radio.getAttribute("aria-checked")).toBe("true")
    screen.getByRole("button", { name: "Downgrade instead" })
  })

  it("links a contact page for missing features only when one is configured", async () => {
    // Stubbed both ways: a developer's .env may set it, CI has none.
    try {
      vi.stubEnv("VITE_CONTACT_URL", "mailto:support@example.com")
      setupApi()
      renderDialog()
      ;(
        await screen.findByRole("button", { name: "Cancel subscription" })
      ).click()
      ;(
        await screen.findByRole("radio", { name: "Missing features I need" })
      ).click()
      const link = await screen.findByRole("link", {
        name: "Tell us what you need",
      })
      expect(link.getAttribute("href")).toBe("mailto:support@example.com")
      expect(link.getAttribute("target")).toBeNull()

      cleanup()
      vi.stubEnv("VITE_CONTACT_URL", "")
      setupApi()
      renderDialog()
      ;(
        await screen.findByRole("button", { name: "Cancel subscription" })
      ).click()
      ;(
        await screen.findByRole("radio", { name: "Missing features I need" })
      ).click()
      expect(
        screen.queryByRole("link", { name: "Tell us what you need" })
      ).toBeNull()
    } finally {
      vi.unstubAllEnvs()
    }
  })

  it("offers nothing while a change is already scheduled or a trial runs", async () => {
    setupApi()
    renderDialog(false)
    ;(
      await screen.findByRole("button", { name: "Cancel subscription" })
    ).click()
    ;(await screen.findByRole("radio", { name: "It's too expensive" })).click()
    await screen.findByText(
      "Downgrading to a lower plan might be a better fit than cancelling entirely."
    )
    expect(
      screen.queryByRole("button", { name: "Downgrade instead" })
    ).toBeNull()
    expect(
      screen.queryByRole("button", { name: "Switch to yearly" })
    ).toBeNull()
  })

  it("shows scheduled-cancellation success copy and an Undo cancellation action", async () => {
    setupApi()
    vi.mocked(api.post).mockResolvedValue(httpResponse({}))
    renderDialog()

    await goThroughToConfirm()

    await screen.findByText("Cancellation scheduled")
    screen.getByText(
      "Your subscription remains active until Sep 1, 2026 and will not renew. You can undo this anytime before then."
    )
    expect(
      screen.queryByRole("button", { name: "Resume subscription" })
    ).toBeNull()

    screen.getByRole("button", { name: "Undo cancellation" }).click()

    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        `/v1/organizations/${organizationId}/billing/cancel/undo`,
        undefined,
        expect.anything()
      )
    )

    // A successful undo must close the dialog instead of leaving it on
    // stale "Cancellation scheduled" copy.
    await waitFor(() =>
      expect(screen.queryByText("Cancellation scheduled")).toBeNull()
    )
  })

  it.each([
    ["activation", true],
    ["subscription", false],
  ] as const)(
    "warns about a pending %s invoice staying owed: %s",
    async (kind, warned) => {
      setupApi([pendingInvoice(kind)])
      renderDialog()

      ;(
        await screen.findByRole("button", { name: "Cancel subscription" })
      ).click()
      ;(await screen.findByRole("radio", { name: "No longer need it" })).click()
      ;(await screen.findByRole("button", { name: "Continue" })).click()
      await screen.findByText("Review cancellation")

      const note =
        "You still have an unpaid invoice. Cancelling doesn't clear it — you can pay it from Billing, or it stays outstanding."
      if (warned) {
        await screen.findByText(note)
      } else {
        // The invoices query must have settled before absence means anything.
        await waitFor(() =>
          expect(
            queryClient.getQueryState(
              queryKeys.billing.invoices(organizationId)
            )?.status
          ).toBe("success")
        )
        expect(screen.queryByText(note)).toBeNull()
      }
    }
  )
})
