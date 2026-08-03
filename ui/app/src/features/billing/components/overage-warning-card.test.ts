// @vitest-environment jsdom
//
// Render-level tests for OverageWarningCard's three states. Uses
// createElement instead of JSX so this stays a .ts file, matching this
// project's other test files (see hooks.test.ts). Mocks the axios client
// (the HTTP boundary useInvoicePreview ultimately calls through), not the
// hook itself, so the test still exercises the real query wiring. No
// jest-dom matchers — this repo doesn't have that dependency, so assertions
// stick to plain DOM/Testing Library APIs (queryByText returns null/element,
// findByText itself throws if nothing matches).
import { createElement, type ReactNode } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { cleanup, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import "@/lib/i18n"

vi.mock("@/lib/api/axios", () => ({
  api: { get: vi.fn() },
}))

import { api } from "@/lib/api/axios"
import { OverageWarningCard } from "./overage-warning-card"

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

let queryClient: QueryClient

beforeEach(() => {
  queryClient = new QueryClient()
  vi.mocked(api.get).mockReset()
})

afterEach(() => {
  cleanup()
})

function wrapper({ children }: { children: ReactNode }) {
  return createElement(QueryClientProvider, { client: queryClient }, children)
}

function renderCard() {
  return render(createElement(OverageWarningCard, { organizationId }), {
    wrapper,
  })
}

describe("OverageWarningCard", () => {
  it("renders nothing when the preview has no overage", async () => {
    vi.mocked(api.get).mockResolvedValue(
      httpResponse({
        plan: "team",
        cycle: "monthly",
        currency: "USD",
        plan_line_cents: 0,
        total_cents: 0,
      })
    )
    const { container } = renderCard()

    await waitFor(() => expect(api.get).toHaveBeenCalled())
    expect(container.firstChild).toBeNull()
  })

  it("renders nothing when overage is present but no metric is actually over", async () => {
    vi.mocked(api.get).mockResolvedValue(
      httpResponse({
        plan: "team",
        cycle: "monthly",
        currency: "USD",
        plan_line_cents: 0,
        total_cents: 0,
        overage: {
          members: { current: 3, allowed: 5, auto_select_removals: [] },
          storage: { current: 100, allowed: -1, auto_select_removals: [] },
        },
      })
    )
    const { container } = renderCard()

    await waitFor(() => expect(api.get).toHaveBeenCalled())
    expect(container.firstChild).toBeNull()
  })

  it("renders only the members metric when only members is over", async () => {
    vi.mocked(api.get).mockResolvedValue(
      httpResponse({
        plan: "team",
        cycle: "monthly",
        currency: "USD",
        plan_line_cents: 0,
        total_cents: 0,
        overage: {
          members: { current: 8, allowed: 5, auto_select_removals: [] },
          storage: { current: 100, allowed: -1, auto_select_removals: [] },
        },
      })
    )
    renderCard()

    await screen.findByText("You have 8 members, but only 5 will be allowed.")
    expect(screen.queryByText(/you're using/i)).toBeNull()
  })

  it("renders only the storage metric when only storage is over", async () => {
    vi.mocked(api.get).mockResolvedValue(
      httpResponse({
        plan: "team",
        cycle: "monthly",
        currency: "USD",
        plan_line_cents: 0,
        total_cents: 0,
        overage: {
          members: { current: 3, allowed: 5, auto_select_removals: [] },
          storage: {
            current: 2_147_483_648,
            allowed: 1_073_741_824,
            auto_select_removals: [],
          },
        },
      })
    )
    renderCard()

    await screen.findByText("You're using 2 GB, but only 1 GB will be allowed.")
    expect(screen.queryByText(/members,/)).toBeNull()
  })

  it("renders both metrics when both are over", async () => {
    vi.mocked(api.get).mockResolvedValue(
      httpResponse({
        plan: "team",
        cycle: "monthly",
        currency: "USD",
        plan_line_cents: 0,
        total_cents: 0,
        overage: {
          members: { current: 8, allowed: 5, auto_select_removals: [] },
          storage: {
            current: 2_147_483_648,
            allowed: 1_073_741_824,
            auto_select_removals: [],
          },
        },
      })
    )
    renderCard()

    await screen.findByText("You have 8 members, but only 5 will be allowed.")
    screen.getByText("You're using 2 GB, but only 1 GB will be allowed.")
    screen.getByText(
      "This will be resolved automatically at renewal — no action needed."
    )
  })
})
