// @vitest-environment jsdom
//
// The MFA gate replaces an organization that requires MFA for a session
// without it: it offers setting up MFA and leaving. createElement keeps this a
// .ts file, like the repo's other tests.
import { createElement } from "react"
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import i18n from "@/lib/i18n"
import { OrganizationMfaGate } from "./organization-mfa-gate"

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }))
vi.mock("@/features/organization/hooks/use-members", () => ({
  useLeaveOrganization: () => ({ mutate: vi.fn(), isPending: false }),
}))
const state = vi.hoisted(() => ({ role: "member" }))
vi.mock("@/hooks/use-active-organization", () => ({
  useActiveOrganization: () => ({ role: state.role }),
}))

afterEach(cleanup)

describe("OrganizationMfaGate", () => {
  it("offers MFA setup and leaving", async () => {
    await i18n.changeLanguage("en")
    render(createElement(OrganizationMfaGate, { organizationId: "org-1" }))
    expect(
      screen.getByText("This organization requires two-factor authentication")
    ).toBeTruthy()
    expect(
      screen.getByRole("button", { name: "Set up two-factor authentication" })
    ).toBeTruthy()
    expect(screen.getByRole("button", { name: "Leave" })).toBeTruthy()
  })

  it("does not offer leaving to the owner", async () => {
    state.role = "owner"
    await i18n.changeLanguage("en")
    render(createElement(OrganizationMfaGate, { organizationId: "org-1" }))
    expect(screen.queryByRole("button", { name: "Leave" })).toBeNull()
    state.role = "member"
  })
})
