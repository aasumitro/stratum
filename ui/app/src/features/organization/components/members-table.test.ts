// @vitest-environment jsdom
//
// Render-level tests for MembersTable's per-member suspend/reinstate wiring:
// an active manageable row exposes a Suspend action that opens a
// confirmation dialog and, once confirmed, calls the suspend mutation with
// that member's auth_sub; a suspended row shows the "Suspended" badge and a
// Reinstate action instead of Suspend; the owner row and the current user's
// own row expose neither. Uses createElement instead of JSX so this stays a
// .ts file, matching this repo's other test files. Mocks the members /
// invitations hook modules (the component's data + mutation boundary) and
// the auth context it reads `user.id` from.
import { createElement } from "react"
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import i18n from "@/lib/i18n"
import type { Member } from "@/types/organization"

const { suspendMock, reinstateMock, removeMock, state } = vi.hoisted(() => ({
  suspendMock: vi.fn(),
  reinstateMock: vi.fn(),
  removeMock: vi.fn(),
  state: { members: [] as Member[] },
}))

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
}))

vi.mock("@/components/auth-provider", () => ({
  useAuth: () => ({ user: { id: "u-self" } }),
}))

vi.mock("@/features/organization/hooks/use-invitations", () => ({
  useOrganizationInvitations: () => ({ data: { data: [] }, isLoading: false }),
  useRevokeInvitation: () => ({ mutate: vi.fn(), isPending: false }),
  useResendInvitation: () => ({ mutate: vi.fn(), isPending: false }),
  useInvite: () => ({ mutate: vi.fn(), isPending: false }),
}))

vi.mock("@/features/organization/hooks/use-members", () => ({
  useOrganizationMembers: () => ({
    data: { data: state.members },
    isLoading: false,
  }),
  useChangeMemberRole: () => ({ mutate: vi.fn(), isPending: false }),
  useRemoveMember: () => ({ mutate: removeMock, isPending: false }),
  useSuspendMember: () => ({ mutate: suspendMock, isPending: false }),
  useReinstateMember: () => ({ mutate: reinstateMock, isPending: false }),
  useLeaveOrganization: () => ({ mutate: vi.fn(), isPending: false }),
}))

import { MembersTable } from "./members-table"

function member(overrides: Partial<Member> & { auth_sub: string }): Member {
  return {
    id: `m-${overrides.auth_sub}`,
    organization_id: "org-1",
    role: "member",
    status: "active",
    joined_at: "2026-01-01T00:00:00Z",
    ...overrides,
  }
}

const owner = member({ auth_sub: "u-owner", role: "owner", full_name: "Olive" })
const self = member({ auth_sub: "u-self", role: "admin", full_name: "Sam" })
const active = member({ auth_sub: "u-active", full_name: "Alex Active" })
const suspended = member({
  auth_sub: "u-susp",
  full_name: "Sue Suspended",
  status: "suspended",
})

function renderTable() {
  return render(
    createElement(MembersTable, {
      organizationId: "org-1",
      currentRole: "owner" as const,
    })
  )
}

function dialogAction(): HTMLButtonElement {
  const btn = document.querySelector<HTMLButtonElement>(
    '[data-slot="alert-dialog-action"]'
  )
  if (!btn) throw new Error("confirmation dialog action button not rendered")
  return btn
}

beforeEach(async () => {
  await i18n.changeLanguage("en")
  suspendMock.mockReset()
  reinstateMock.mockReset()
  removeMock.mockReset()
})

afterEach(() => {
  cleanup()
})

describe("MembersTable — per-member suspend/reinstate", () => {
  it("active manageable row: Suspend → confirm calls the suspend mutation with the auth_sub", async () => {
    state.members = [owner, self, active]
    renderTable()

    const suspendButtons = screen.getAllByRole("button", { name: "Suspend" })
    expect(suspendButtons.length).toBeGreaterThan(0)
    expect(screen.queryByRole("button", { name: "Reinstate" })).toBeNull()

    suspendButtons[0].click()

    await screen.findByText("Suspend member?")
    dialogAction().click()

    expect(suspendMock).toHaveBeenCalledWith(
      "u-active",
      expect.objectContaining({ onSuccess: expect.any(Function) })
    )
  })

  it("suspended row: renders the Suspended badge and a Reinstate action instead of Suspend", async () => {
    state.members = [owner, self, suspended]
    renderTable()

    expect(screen.getAllByText("Suspended").length).toBeGreaterThan(0)
    expect(screen.queryByRole("button", { name: "Suspend" })).toBeNull()

    const reinstateButtons = screen.getAllByRole("button", {
      name: "Reinstate",
    })
    expect(reinstateButtons.length).toBeGreaterThan(0)

    reinstateButtons[0].click()

    await screen.findByText("Reinstate member?")
    dialogAction().click()

    expect(reinstateMock).toHaveBeenCalledWith(
      "u-susp",
      expect.objectContaining({ onSuccess: expect.any(Function) })
    )
  })

  it("owner row and current-user row expose neither Suspend nor Reinstate", () => {
    state.members = [owner, self]
    renderTable()

    expect(screen.queryByRole("button", { name: "Suspend" })).toBeNull()
    expect(screen.queryByRole("button", { name: "Reinstate" })).toBeNull()
  })
})
