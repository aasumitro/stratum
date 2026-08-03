// @vitest-environment jsdom
//
// Hook-level tests for the billing mutation hooks: each fires the right HTTP
// method/path and invalidates exactly the query keys it should. Uses
// createElement instead of JSX so this stays a .ts file (this project's
// existing test files are all plain .ts; renderHook doesn't need a visible
// component, just a QueryClientProvider ancestor for useMutation to work).
import { createElement, type ReactNode } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { cleanup, renderHook, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import "@/lib/i18n" // side-effect: initializes the global i18next instance useTranslation() reads

vi.mock("@/lib/api/axios", () => ({
  api: { post: vi.fn(), delete: vi.fn(), put: vi.fn(), patch: vi.fn() },
}))

import { api } from "@/lib/api/axios"
import { queryKeys } from "@/lib/api/keys"
import {
  useAttachAddon,
  useCancelSubscription,
  useDetachAddon,
  useDowngradeSubscription,
  useUndoScheduledAddonChange,
  useUndoScheduledCancellation,
  useUndoScheduledDowngrade,
} from "./hooks"

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
let invalidateSpy: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  queryClient = new QueryClient()
  invalidateSpy = vi.spyOn(queryClient, "invalidateQueries")
  vi.mocked(api.post).mockReset()
  vi.mocked(api.delete).mockReset()
})

afterEach(() => {
  cleanup()
})

function wrapper({ children }: { children: ReactNode }) {
  return createElement(QueryClientProvider, { client: queryClient }, children)
}

function invalidatedKeys() {
  return invalidateSpy.mock.calls.map(
    (call: Parameters<QueryClient["invalidateQueries"]>) => call[0]?.queryKey
  )
}

describe("useUndoScheduledDowngrade", () => {
  it("POSTs .../downgrade/undo and invalidates subscription + preview", async () => {
    vi.mocked(api.post).mockResolvedValue(httpResponse(null))
    const { result } = renderHook(
      () => useUndoScheduledDowngrade(organizationId),
      {
        wrapper,
      }
    )

    result.current.mutate()
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(api.post).toHaveBeenCalledWith(
      `/v1/organizations/${organizationId}/billing/downgrade/undo`,
      undefined,
      expect.anything()
    )
    expect(invalidatedKeys()).toEqual(
      expect.arrayContaining([
        queryKeys.billing.subscription(organizationId),
        queryKeys.billing.preview(organizationId),
      ])
    )
    expect(invalidateSpy).toHaveBeenCalledTimes(2)
  })
})

describe("useUndoScheduledCancellation", () => {
  it("POSTs .../cancel/undo and invalidates subscription + preview", async () => {
    vi.mocked(api.post).mockResolvedValue(httpResponse(null))
    const { result } = renderHook(
      () => useUndoScheduledCancellation(organizationId),
      { wrapper }
    )

    result.current.mutate()
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(api.post).toHaveBeenCalledWith(
      `/v1/organizations/${organizationId}/billing/cancel/undo`,
      undefined,
      expect.anything()
    )
    expect(invalidatedKeys()).toEqual(
      expect.arrayContaining([
        queryKeys.billing.subscription(organizationId),
        queryKeys.billing.preview(organizationId),
      ])
    )
    expect(invalidateSpy).toHaveBeenCalledTimes(2)
  })
})

describe("useUndoScheduledAddonChange", () => {
  it("POSTs .../addons/:addonID/undo and invalidates addons + usage + preview", async () => {
    vi.mocked(api.post).mockResolvedValue(httpResponse(null))
    const { result } = renderHook(
      () => useUndoScheduledAddonChange(organizationId),
      { wrapper }
    )

    result.current.mutate("extra-seat")
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(api.post).toHaveBeenCalledWith(
      `/v1/organizations/${organizationId}/billing/addons/extra-seat/undo`,
      "extra-seat",
      expect.anything()
    )
    expect(invalidatedKeys()).toEqual(
      expect.arrayContaining([
        queryKeys.billing.addons(organizationId),
        queryKeys.billing.usage(organizationId),
        queryKeys.billing.preview(organizationId),
      ])
    )
    expect(invalidateSpy).toHaveBeenCalledTimes(3)
    // Undoing a scheduled addon change never touched the subscription row
    // itself — only the addon row — so it must not invalidate that query.
    expect(invalidatedKeys()).not.toEqual(
      expect.arrayContaining([queryKeys.billing.subscription(organizationId)])
    )
  })
})

describe("useCancelSubscription", () => {
  it("also invalidates the preview query key alongside subscription", async () => {
    vi.mocked(api.post).mockResolvedValue(httpResponse(null))
    const { result } = renderHook(() => useCancelSubscription(organizationId), {
      wrapper,
    })

    result.current.mutate({ reason: "too_expensive" })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(invalidatedKeys()).toEqual(
      expect.arrayContaining([
        queryKeys.billing.subscription(organizationId),
        queryKeys.billing.preview(organizationId),
      ])
    )
  })
})

describe("useDowngradeSubscription", () => {
  it("also invalidates the preview query key alongside its existing set", async () => {
    vi.mocked(api.post).mockResolvedValue(httpResponse(null))
    const { result } = renderHook(
      () => useDowngradeSubscription(organizationId),
      { wrapper }
    )

    result.current.mutate({ plan: "solo", cycle: "monthly" })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(invalidatedKeys()).toEqual(
      expect.arrayContaining([
        queryKeys.billing.subscription(organizationId),
        queryKeys.billing.invoices(organizationId),
        queryKeys.billing.paymentLinks(organizationId),
        queryKeys.billing.history(organizationId),
        queryKeys.billing.preview(organizationId),
      ])
    )
  })
})

describe("useAttachAddon", () => {
  it("also invalidates the preview query key alongside its existing set", async () => {
    vi.mocked(api.post).mockResolvedValue(httpResponse(null))
    const { result } = renderHook(() => useAttachAddon(organizationId), {
      wrapper,
    })

    result.current.mutate({ addon_id: "extra-seat", quantity: 1 })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(invalidatedKeys()).toEqual(
      expect.arrayContaining([
        queryKeys.billing.addons(organizationId),
        queryKeys.billing.subscription(organizationId),
        queryKeys.billing.preview(organizationId),
      ])
    )
  })
})

describe("useDetachAddon", () => {
  it("also invalidates the preview query key alongside its existing set", async () => {
    vi.mocked(api.delete).mockResolvedValue(httpResponse(null))
    const { result } = renderHook(() => useDetachAddon(organizationId), {
      wrapper,
    })

    result.current.mutate("extra-seat")
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(api.delete).toHaveBeenCalledWith(
      `/v1/organizations/${organizationId}/billing/addons/extra-seat`,
      expect.anything()
    )
    expect(invalidatedKeys()).toEqual(
      expect.arrayContaining([
        queryKeys.billing.addons(organizationId),
        queryKeys.billing.subscription(organizationId),
        queryKeys.billing.preview(organizationId),
      ])
    )
  })
})
