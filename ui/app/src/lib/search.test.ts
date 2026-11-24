import { describe, expect, it } from "vitest"
import { searchTerm } from "./search"

describe("searchTerm", () => {
  it("sends nothing until three characters are typed", () => {
    expect(searchTerm("")).toBe("")
    expect(searchTerm(" ab ")).toBe("")
    expect(searchTerm(" abc ")).toBe("abc")
    expect(searchTerm("é€")).toBe("")
    expect(searchTerm("é€x")).toBe("é€x")
  })
})
