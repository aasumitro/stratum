// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from "vitest"
import i18n from "@/lib/i18n"
import { AxiosError, AxiosHeaders } from "axios"
import {
  ApiError,
  catchHTTPError,
  isRefusal,
  isTransientError,
  parseApiError,
  SERVER_ERROR,
} from "./error"

describe("parseApiError", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en")
  })

  it("translates a known code via the errors.codes catalog", () => {
    const err = {
      status: { code: "EMAIL_REQUIRED", message: "raw backend text" },
    }
    expect(parseApiError(err, "fallback")).toBe(
      i18n.t("errors.codes.EMAIL_REQUIRED")
    )
  })

  it("falls back to the backend message when the code has no translation", () => {
    const err = {
      status: { code: "SOME_UNKNOWN_CODE", message: "raw backend text" },
    }
    expect(parseApiError(err, "fallback")).toBe("raw backend text")
  })

  it("falls back to the backend message when there is no code at all", () => {
    const err = { status: { message: "raw backend text" } }
    expect(parseApiError(err, "fallback")).toBe("raw backend text")
  })

  it("uses the caller-supplied fallback when neither message nor code is present", () => {
    expect(parseApiError({}, "fallback")).toBe("fallback")
  })

  it("renders in Indonesian once the language is switched", async () => {
    await i18n.changeLanguage("id")
    const err = {
      status: { code: "EMAIL_REQUIRED", message: "raw backend text" },
    }
    expect(parseApiError(err, "fallback")).toBe(
      "Akun Anda belum memiliki email terverifikasi."
    )
  })
})

describe("isTransientError", () => {
  it("is true for an outage the server may recover from", () => {
    expect(isTransientError(new ApiError({}, 503))).toBe(true)
    expect(isTransientError(new ApiError({}, 502))).toBe(true)
    expect(isTransientError(new ApiError({}, 504))).toBe(true)
    expect(isTransientError(new Error(SERVER_ERROR.CONNECTION))).toBe(true)
  })

  it("is false for a refusal or a server bug, which answer the same every time", () => {
    for (const status of [400, 401, 403, 404, 422, 500]) {
      expect(isTransientError(new ApiError({}, status))).toBe(false)
    }
    expect(isTransientError(new ApiError({}))).toBe(false)
    expect(isTransientError(new Error("boom"))).toBe(false)
  })
})

describe("isRefusal", () => {
  it("is true only for 401, 403 and 404", () => {
    for (const status of [401, 403, 404]) {
      expect(isRefusal(new ApiError({}, status))).toBe(true)
    }
    for (const status of [400, 422, 429, 500, 502, 503]) {
      expect(isRefusal(new ApiError({}, status))).toBe(false)
    }
    expect(isRefusal(new Error(SERVER_ERROR.CONNECTION))).toBe(false)
  })
})

describe("catchHTTPError", () => {
  it("keeps the status of a response with no JSON body", () => {
    const err = new AxiosError(
      "bad gateway",
      "ERR_BAD_RESPONSE",
      undefined,
      undefined,
      {
        status: 502,
        statusText: "Bad Gateway",
        data: "<html>bad gateway</html>",
        headers: {},
        config: { headers: new AxiosHeaders() },
      }
    )
    let thrown: unknown
    try {
      catchHTTPError(err)
    } catch (e) {
      thrown = e
    }
    expect(thrown).toBeInstanceOf(ApiError)
    expect(isTransientError(thrown)).toBe(true)
  })
})
