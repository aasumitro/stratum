import { describe, expect, it } from "vitest"
import { hasPermission, PERMISSION_MATRIX } from "./permissions-matrix"
import type {
  OrganizationRole,
  PermissionAction,
  PermissionFeature,
} from "@/types/organization"

const ROLE_ORDER: OrganizationRole[] = ["member", "admin", "owner"]

describe("hasPermission", () => {
  for (const feature of Object.keys(PERMISSION_MATRIX) as PermissionFeature[]) {
    const actions = PERMISSION_MATRIX[feature]
    for (const action of Object.keys(actions) as PermissionAction[]) {
      const requiredRole = actions[action]!
      const requiredIdx = ROLE_ORDER.indexOf(requiredRole)

      it(`${feature}.${action} requires at least "${requiredRole}"`, () => {
        for (const [idx, role] of ROLE_ORDER.entries()) {
          expect(hasPermission(role, feature, action)).toBe(idx >= requiredIdx)
        }
      })
    }
  }

  it("denies an action a feature never defines", () => {
    expect(hasPermission("owner", "settingsGeneral", "delete")).toBe(false)
  })

  it("denies when role is undefined", () => {
    expect(hasPermission(undefined, "members", "view")).toBe(false)
  })
})
