// @vitest-environment jsdom
//
// Render-level test for ActivateTrialDialog's success step: the answer carries the
// invoice to pay, or nothing when a coupon covered the whole charge, and then the
// dialog must not send the owner looking for an invoice that does not exist. Mocks the
// axios client, the HTTP boundary every hook here ultimately calls through.
import { createElement } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { cleanup, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import i18n from "@/lib/i18n"

vi.mock("@/lib/api/axios", () => ({
  api: { get: vi.fn(), post: vi.fn() },
}))

import { api } from "@/lib/api/axios"
import { ActivateTrialDialog } from "./activate-trial-dialog"

const payload = <T>(data: T) => ({
  data: { data, status: { request_id: "r", error: false, message: "" } },
})

async function activate() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    createElement(
      QueryClientProvider,
      { client },
      createElement(ActivateTrialDialog, { organizationId: "org-1" })
    )
  )
  fireEvent.click(
    screen.getByRole("button", { name: i18n.t("billing.trial.activateNow") })
  )
  const dialog = await screen.findByRole("dialog")
  const confirm = Array.from(dialog.querySelectorAll("button")).find(
    (b) => b.textContent === i18n.t("billing.trial.activateNow")
  )
  await vi.waitFor(() => expect(confirm).toHaveProperty("disabled", false))
  fireEvent.click(confirm as HTMLButtonElement)
}

describe("ActivateTrialDialog success step", () => {
  beforeEach(() => {
    vi.mocked(api.get).mockResolvedValue(payload(null))
  })

  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it("shows the invoice to pay", async () => {
    vi.mocked(api.post).mockResolvedValue(
      payload({
        id: "inv-1",
        amount_cents: 5000,
        currency: "USD",
        due_at: "2026-12-01T00:00:00Z",
      })
    )
    await activate()
    expect(
      await screen.findByText(
        i18n.t("billing.trial.activateSuccessDescription") as string
      )
    ).toBeTruthy()
    expect(
      screen.getByText(i18n.t("billing.extend.amountDueLabel") as string)
    ).toBeTruthy()
  })

  it("has nothing to pay when a coupon covered the charge", async () => {
    vi.mocked(api.post).mockResolvedValue(payload(null))
    await activate()
    expect(
      await screen.findByText(
        i18n.t("billing.trial.activateCoveredDescription") as string
      )
    ).toBeTruthy()
    expect(
      screen.queryByText(i18n.t("billing.extend.amountDueLabel") as string)
    ).toBeNull()
  })
})
