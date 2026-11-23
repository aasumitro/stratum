// @vitest-environment jsdom
import { describe, expect, it } from "vitest"
import { ApiError } from "@/lib/api/error"
import { shouldRetryQuery } from "./query"

describe("shouldRetryQuery", () => {
  it("retries an outage up to three times", () => {
    const outage = new ApiError({}, 503)
    expect(shouldRetryQuery(0, outage)).toBe(true)
    expect(shouldRetryQuery(2, outage)).toBe(true)
    expect(shouldRetryQuery(3, outage)).toBe(false)
  })

  it("never retries a refusal or a server bug", () => {
    expect(shouldRetryQuery(0, new ApiError({}, 403))).toBe(false)
    expect(shouldRetryQuery(0, new ApiError({}, 404))).toBe(false)
    expect(shouldRetryQuery(0, new ApiError({}, 500))).toBe(false)
  })
})
