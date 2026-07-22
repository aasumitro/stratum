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

/** Maps a 422 response's `status.details` onto TanStack Form field errors. */
export function applyServerErrors(
  form: ServerErrorForm,
  details: Record<string, string[]>
) {
  for (const [field, messages] of Object.entries(details)) {
    form.setFieldMeta(field as never, (prev) => ({ ...prev, errors: messages }))
  }
}
