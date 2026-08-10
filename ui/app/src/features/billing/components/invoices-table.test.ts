// @vitest-environment jsdom
//
// Regression test for the addon_increase invoice-kind badge: an entry
// missing from billing.invoices.kind renders the raw i18next key instead of
// a label — invoices-table.tsx:203's t(`billing.invoices.kind.${inv.kind}`)
// must resolve addon_increase to a real translated string.
import { describe, expect, it } from "vitest"
import i18n from "@/lib/i18n" // side-effect: initializes the global i18next instance

describe("billing.invoices.kind.addon_increase", () => {
  it("resolves to a translated label in en, not the raw key", async () => {
    await i18n.changeLanguage("en")
    expect(i18n.t("billing.invoices.kind.addon_increase")).toBe(
      "Addon increase"
    )
  })

  it("resolves to a translated label in id, not the raw key", async () => {
    await i18n.changeLanguage("id")
    expect(i18n.t("billing.invoices.kind.addon_increase")).toBe(
      "Penambahan add-on"
    )
  })
})
