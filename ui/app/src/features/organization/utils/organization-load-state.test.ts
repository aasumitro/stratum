// @vitest-environment jsdom
import { describe, expect, it } from "vitest"
import { ApiError } from "@/lib/api/error"
import { organizationLoadState } from "./organization-load-state"

const mfa = new ApiError(
  { status: { code: "ORGANIZATION_MFA_REQUIRED", message: "x" } } as never,
  403
)

describe("organizationLoadState", () => {
  it("shows the MFA gate for the policy's 403, never the refusal path", () => {
    expect(organizationLoadState(true, mfa, false)).toBe("mfa")
    expect(organizationLoadState(true, mfa, true)).toBe("mfa")
  })

  it("leaves only on a refusal", () => {
    expect(organizationLoadState(true, new ApiError({}, 403), true)).toBe(
      "refused"
    )
    expect(organizationLoadState(true, new ApiError({}, 404), false)).toBe(
      "refused"
    )
  })

  it("offers a retry for any other failure with nothing loaded, and keeps a loaded page", () => {
    expect(organizationLoadState(true, new ApiError({}, 503), false)).toBe(
      "unavailable"
    )
    expect(organizationLoadState(true, new ApiError({}, 500), true)).toBe("ok")
    expect(organizationLoadState(false, null, true)).toBe("ok")
  })
})
