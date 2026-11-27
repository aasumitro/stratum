// @vitest-environment jsdom
//
// The consent gate blocks the app while a required document's current version
// is not accepted, and disappears once everything is. createElement keeps this
// a .ts file, like the repo's other tests.
import { createElement } from "react"
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import i18n from "@/lib/i18n"
import type { ConsentStatus } from "@/types/account"

const state = vi.hoisted(() => ({ items: [] as ConsentStatus[] }))

vi.mock("@/components/auth-provider", () => ({
  useAuth: () => ({ signOut: vi.fn() }),
}))
vi.mock("@/features/account/hooks", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/features/account/hooks")>()
  return {
    pendingConsents: actual.pendingConsents,
    useConsents: () => ({ data: { data: state.items } }),
    useAcceptConsents: () => ({ accept: vi.fn(), isPending: false }),
  }
})

const { ConsentGate } = await import("./consent-gate")
const { pendingConsents } = await import("@/features/account/hooks")

afterEach(cleanup)

const terms: ConsentStatus = {
  document: "terms",
  version: "2026-11-01",
  url: "https://example.com/terms",
}

describe("pendingConsents", () => {
  it("keeps documents whose current version is not accepted", () => {
    expect(pendingConsents([terms])).toHaveLength(1)
    expect(
      pendingConsents([{ ...terms, accepted_version: "2026-01-01" }])
    ).toHaveLength(1)
    expect(
      pendingConsents([{ ...terms, accepted_version: "2026-11-01" }])
    ).toHaveLength(0)
    expect(pendingConsents(null)).toHaveLength(0)
  })
})

describe("ConsentGate", () => {
  it("blocks with links to the pending documents", async () => {
    await i18n.changeLanguage("en")
    state.items = [terms]
    render(createElement(ConsentGate))
    expect(screen.getByText("Accept the terms to continue")).toBeTruthy()
    const link = screen.getByRole("link", { name: "Terms of Service" })
    expect(link.getAttribute("href")).toBe("https://example.com/terms")
  })

  it("renders nothing once every document is accepted", () => {
    state.items = [{ ...terms, accepted_version: "2026-11-01" }]
    render(createElement(ConsentGate))
    expect(screen.queryByText("Accept the terms to continue")).toBeNull()
  })
})
