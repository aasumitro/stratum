// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from "vitest"
import i18n from "@/lib/i18n"
import { parseApiError } from "./error"

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
