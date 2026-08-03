// @vitest-environment jsdom
//
// Render-level tests for ReviewChangesDialog's diff-splitting and confirm
// behavior. Uses createElement instead of JSX so this stays a .ts file,
// matching this repo's other test files. Mocks the axios client, the same
// HTTP-boundary-mock convention hooks.test.ts/overage-warning-card.test.ts
// already established.
import { createElement, type ReactNode } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { cleanup, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import "@/lib/i18n"

vi.mock("@/lib/api/axios", () => ({
  api: { post: vi.fn(), delete: vi.fn() },
}))

import { api } from "@/lib/api/axios"
import { ReviewChangesDialog } from "./review-changes-dialog"
import type { AttachedAddon } from "@/types/billing"
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
    description: "",
    prices: {},
    features: {},
    active: true,
    created_at: "",
  },
  {
    id: "extra-storage-1gb",
    name: "Extra Storage",
    description: "",
    prices: {},
    features: {},
    active: true,
    created_at: "",
  },
]

function attached(overrides: Partial<AttachedAddon> = {}): AttachedAddon {
  return {
    addon_id: "extra-seat",
    name: "Extra Seat",
    quantity: 3,
    prices: {},
    ...overrides,
  }
}

let queryClient: QueryClient

beforeEach(() => {
  queryClient = new QueryClient()
  vi.mocked(api.post).mockReset()
  vi.mocked(api.delete).mockReset()
})

afterEach(() => {
  cleanup()
})

function wrapper({ children }: { children: ReactNode }) {
  return createElement(QueryClientProvider, { client: queryClient }, children)
}

function renderDialog(props: {
  staged: Record<string, number>
  currentlyAttached: AttachedAddon[]
  onOpenChange?: (open: boolean) => void
}) {
  return render(
    createElement(ReviewChangesDialog, {
      open: true,
      onOpenChange: props.onOpenChange ?? (() => {}),
      staged: props.staged,
      currentlyAttached: props.currentlyAttached,
      catalog,
      periodEnd: "2026-09-01T00:00:00Z",
      organizationId,
    }),
    { wrapper }
  )
}

describe("ReviewChangesDialog", () => {
  it("shows only the pending-payment section for a brand-new attach", async () => {
    renderDialog({ staged: { "extra-seat": 2 }, currentlyAttached: [] })

    await screen.findByText(
      "Creates an invoice — your limit rises once it's paid"
    )
    expect(screen.getByText("Extra Seat: 0 → 2")).toBeTruthy()
    expect(screen.queryByText(/Effective at renewal/)).toBeNull()
  })

  it("shows only the scheduled section for a decrease, naming the renewal date", async () => {
    renderDialog({
      staged: { "extra-seat": 1 },
      currentlyAttached: [attached({ quantity: 3 })],
    })

    await screen.findByText("Effective at renewal on Sep 1, 2026")
    expect(screen.getByText("Extra Seat: 3 → 1")).toBeTruthy()
    expect(
      screen.queryByText("Creates an invoice — your limit rises once it's paid")
    ).toBeNull()
  })

  it("shows both sections for a mixed increase + decrease selection", async () => {
    renderDialog({
      staged: { "extra-seat": 5, "extra-storage-1gb": 1 },
      currentlyAttached: [
        attached({ addon_id: "extra-seat", quantity: 3 }),
        attached({
          addon_id: "extra-storage-1gb",
          name: "Extra Storage",
          quantity: 4,
        }),
      ],
    })

    await screen.findByText(
      "Creates an invoice — your limit rises once it's paid"
    )
    expect(screen.getByText("Extra Seat: 3 → 5")).toBeTruthy()
    expect(screen.getByText("Effective at renewal on Sep 1, 2026")).toBeTruthy()
    expect(screen.getByText("Extra Storage: 4 → 1")).toBeTruthy()
  })

  it("confirming fires attach for the immediate bucket and detach for a full removal", async () => {
    vi.mocked(api.post).mockResolvedValue(httpResponse(null))
    vi.mocked(api.delete).mockResolvedValue(httpResponse(null))
    const onOpenChange = vi.fn()
    renderDialog({
      staged: { "extra-storage-1gb": 6 },
      currentlyAttached: [
        attached({ addon_id: "extra-seat", quantity: 3 }),
        attached({
          addon_id: "extra-storage-1gb",
          name: "Extra Storage",
          quantity: 4,
        }),
      ],
      onOpenChange,
    })

    const confirmButton = await screen.findByRole("button", {
      name: "Confirm changes",
    })
    confirmButton.click()

    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        `/v1/organizations/${organizationId}/billing/addons`,
        { addon_id: "extra-storage-1gb", quantity: 6 },
        expect.anything()
      )
    )
    expect(api.delete).toHaveBeenCalledWith(
      `/v1/organizations/${organizationId}/billing/addons/extra-seat`,
      expect.anything()
    )
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it("keeps the dialog open and shows an error when one of several changes fails", async () => {
    vi.mocked(api.post).mockResolvedValue(httpResponse(null))
    vi.mocked(api.delete).mockRejectedValue(new Error("network error"))
    const onOpenChange = vi.fn()
    renderDialog({
      staged: { "extra-storage-1gb": 6 },
      currentlyAttached: [
        attached({ addon_id: "extra-seat", quantity: 3 }),
        attached({
          addon_id: "extra-storage-1gb",
          name: "Extra Storage",
          quantity: 4,
        }),
      ],
      onOpenChange,
    })

    const confirmButton = await screen.findByRole("button", {
      name: "Confirm changes",
    })
    confirmButton.click()

    await screen.findByText(
      "Some changes couldn't be applied. Try confirming again."
    )
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
  })
})
