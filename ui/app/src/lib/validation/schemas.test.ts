import { describe, expect, it } from "vitest"
import {
  createEmailSchema,
  createExactLengthCodeSchema,
  createOptionalHandleSchema,
  createPasswordSchema,
  createRequiredHandleSchema,
  createRequiredTextSchema,
  fieldValidator,
} from "./schemas"

describe("createEmailSchema", () => {
  const validate = fieldValidator(
    createEmailSchema({ required: "required", invalid: "invalid" })
  )
  it("rejects empty", () => expect(validate({ value: "" })).toBe("required"))
  it("rejects malformed", () =>
    expect(validate({ value: "not-an-email" })).toBe("invalid"))
  it("accepts a valid email", () =>
    expect(validate({ value: "a@b.com" })).toBeUndefined())
})

describe("createRequiredTextSchema", () => {
  const validate = fieldValidator(createRequiredTextSchema("required"))
  it("rejects empty/whitespace", () => {
    expect(validate({ value: "" })).toBe("required")
    expect(validate({ value: "   " })).toBe("required")
  })
  it("accepts non-empty", () =>
    expect(validate({ value: "Acme" })).toBeUndefined())
})

describe("createPasswordSchema", () => {
  it("required only", () => {
    const validate = fieldValidator(
      createPasswordSchema({ required: "required" })
    )
    expect(validate({ value: "" })).toBe("required")
    expect(validate({ value: "x" })).toBeUndefined()
  })
  it("required + min length, required wins on empty", () => {
    const validate = fieldValidator(
      createPasswordSchema({
        required: "required",
        minLength: { length: 8, message: "too short" },
      })
    )
    expect(validate({ value: "" })).toBe("required")
    expect(validate({ value: "short" })).toBe("too short")
    expect(validate({ value: "longenough" })).toBeUndefined()
  })
})

describe("createOptionalHandleSchema", () => {
  const validate = fieldValidator(createOptionalHandleSchema("invalid"))
  it("allows empty", () => expect(validate({ value: "" })).toBeUndefined())
  it("rejects a disallowed character when non-empty", () =>
    expect(validate({ value: "not valid!" })).toBe("invalid"))
  it("accepts an allowed handle", () =>
    expect(validate({ value: "john.doe_1" })).toBeUndefined())
})

describe("createRequiredHandleSchema", () => {
  const validate = fieldValidator(
    createRequiredHandleSchema({ minLength: "too short", invalid: "invalid" })
  )
  it("rejects under min length before checking the pattern", () =>
    expect(validate({ value: "a" })).toBe("too short"))
  it("rejects a disallowed character once long enough", () =>
    expect(validate({ value: "not valid" })).toBe("invalid"))
  it("accepts a valid handle", () =>
    expect(validate({ value: "john.doe" })).toBeUndefined())
})

describe("createExactLengthCodeSchema", () => {
  const validate = fieldValidator(
    createExactLengthCodeSchema({
      required: "required",
      length: 16,
      lengthMessage: "wrong length",
    })
  )
  it("rejects empty", () => expect(validate({ value: "" })).toBe("required"))
  it("rejects wrong length", () =>
    expect(validate({ value: "short" })).toBe("wrong length"))
  it("accepts the exact length", () =>
    expect(validate({ value: "a".repeat(16) })).toBeUndefined())
})
