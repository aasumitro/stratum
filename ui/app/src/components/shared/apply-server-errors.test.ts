// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest"
import i18n from "@/lib/i18n"
import { applyServerErrors } from "./apply-server-errors"

describe("applyServerErrors", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en")
  })

  it("translates a known validator code and interpolates the humanized field name + param", () => {
    const setFieldMeta = vi.fn()
    applyServerErrors(
      { setFieldMeta },
      { first_name: [{ code: "min", param: "3" }] }
    )
    expect(setFieldMeta).toHaveBeenCalledTimes(1)
    const [field, updater] = setFieldMeta.mock.calls[0] as [
      string,
      (prev: { errors: unknown[] }) => { errors: unknown[] },
    ]
    expect(field).toBe("first_name")
    expect(updater({ errors: [] }).errors).toEqual([
      "The First Name field must be at least 3.",
    ])
  })

  it("falls back to the default message for an unrecognized code", () => {
    const setFieldMeta = vi.fn()
    applyServerErrors({ setFieldMeta }, { slug: [{ code: "some_new_tag" }] })
    const updater = setFieldMeta.mock.calls[0][1] as (prev: {
      errors: unknown[]
    }) => { errors: unknown[] }
    expect(updater({ errors: [] }).errors).toEqual([
      "The Slug field is invalid.",
    ])
  })

  it("renders in Indonesian once the language is switched", async () => {
    await i18n.changeLanguage("id")
    const setFieldMeta = vi.fn()
    applyServerErrors({ setFieldMeta }, { email: [{ code: "required" }] })
    const updater = setFieldMeta.mock.calls[0][1] as (prev: {
      errors: unknown[]
    }) => { errors: unknown[] }
    expect(updater({ errors: [] }).errors).toEqual(["Kolom Email wajib diisi."])
  })
})
