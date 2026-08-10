import i18n from "@/lib/i18n"

// The standard 422 -> field-error mapping pattern for this app's forms, but
// never actually centralized — every form re-implements (or skips) it.
interface ServerErrorForm {
  setFieldMeta: (
    field: never,
    updater: (
      prev: { errors: unknown[] } & Record<string, unknown>
    ) => { errors: unknown[] } & Record<string, unknown>
  ) => void
}

/** One field-level validation failure as the backend sends it — Code is the
 * validator tag ("required", "min", ...), Param is that tag's argument
 * where it has one (e.g. "8" for "min=8"). */
export interface FieldError {
  code: string
  param?: string
}

function humanizeField(field: string): string {
  return field
    .split("_")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ")
}

/** Maps a 422 response's `status.details` onto TanStack Form field errors,
 * translating each field's validator-tag code via the errors.validation.*
 * i18n catalog (falling back to errors.validation.default for a tag with no
 * dedicated entry). */
export function applyServerErrors(
  form: ServerErrorForm,
  details: Record<string, FieldError[]>
) {
  for (const [field, fieldErrors] of Object.entries(details)) {
    const label = humanizeField(field)
    const messages = fieldErrors.map(({ code, param }) =>
      i18n.t(`errors.validation.${code}`, {
        field: label,
        param,
        defaultValue: i18n.t("errors.validation.default", { field: label }),
      })
    )
    form.setFieldMeta(field as never, (prev) => ({ ...prev, errors: messages }))
  }
}
