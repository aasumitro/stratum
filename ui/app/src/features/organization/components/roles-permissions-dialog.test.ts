import { describe, expect, it } from "vitest"
import en from "@/lib/i18n/locales/en.json"
import id from "@/lib/i18n/locales/id.json"
import { PERMISSION_MATRIX } from "@/lib/permissions-matrix"

// The roles reference renders a row per (feature, action) in the permission
// matrix; every one needs a label in each locale, or the dialog shows raw keys.
describe("roles & permissions labels", () => {
  for (const [lang, locale] of [
    ["en", en],
    ["id", id],
  ] as const) {
    it(`has a ${lang} label for every feature and action in the matrix`, () => {
      const labels = locale.organization.permissions
      for (const [feature, actions] of Object.entries(PERMISSION_MATRIX)) {
        expect(labels.features).toHaveProperty(feature)
        for (const action of Object.keys(actions)) {
          expect(labels.actions).toHaveProperty(action)
        }
      }
    })
  }
})
