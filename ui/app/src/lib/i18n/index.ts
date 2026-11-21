import i18n from "i18next"
import { initReactI18next } from "react-i18next"
import en from "./locales/en.json"
import id from "./locales/id.json"

const savedLang = localStorage.getItem("lang")
const browserLang = navigator.language.startsWith("id") ? "id" : "en"

void i18n.use(initReactI18next).init({
  resources: {
    en: { translation: en },
    id: { translation: id },
  },
  lng: savedLang ?? browserLang,
  fallbackLng: "en",
  interpolation: { escapeValue: false },
})

export default i18n

// The translate function the plain helpers take: what both i18next's TFunction
// (from useTranslation or i18n.t) and a test's fake (key) => key satisfy.
// TFunction itself cannot be faked, and since i18next 27 it no longer fits a
// single (key, options?) signature.
export type Translate = {
  (key: string): string
  (key: string, options: Record<string, unknown>): string
}
