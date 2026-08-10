import { z } from "zod"

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
const HANDLE_RE = /^[A-Za-z0-9._]+$/

/** Wraps a Zod schema as a TanStack Form field validator: same
 * `string | undefined` contract every hand-rolled validator in this app
 * already returned, so field error-rendering JSX (`errors[0]`) needs no
 * change — TanStack Form's own Standard Schema support instead exposes raw
 * issue objects on `meta.errors`, which every field here renders directly
 * as a string. */
export function fieldValidator<T>(schema: z.ZodType<T>) {
  return ({ value }: { value: T }) => {
    const result = schema.safeParse(value)
    return result.success ? undefined : result.error.issues[0]?.message
  }
}

export function createEmailSchema(messages: {
  required: string
  invalid: string
}) {
  return z.string().superRefine((v, ctx) => {
    if (!v) ctx.addIssue({ code: "custom", message: messages.required })
    else if (!EMAIL_RE.test(v))
      ctx.addIssue({ code: "custom", message: messages.invalid })
  })
}

export function createRequiredTextSchema(message: string) {
  return z.string().superRefine((v, ctx) => {
    if (!v.trim()) ctx.addIssue({ code: "custom", message })
  })
}

export function createPasswordSchema(messages: {
  required: string
  minLength?: { length: number; message: string }
}) {
  return z.string().superRefine((v, ctx) => {
    if (!v) ctx.addIssue({ code: "custom", message: messages.required })
    else if (messages.minLength && v.length < messages.minLength.length)
      ctx.addIssue({ code: "custom", message: messages.minLength.message })
  })
}

/** An optional handle-like field (display name) — empty is fine, but a
 * non-empty value must match the allowed character set. */
export function createOptionalHandleSchema(invalidMessage: string) {
  return z.string().superRefine((v, ctx) => {
    if (v && !HANDLE_RE.test(v))
      ctx.addIssue({ code: "custom", message: invalidMessage })
  })
}

/** A required handle-like field (display name) — minimum length, then the
 * allowed character set. */
export function createRequiredHandleSchema(messages: {
  minLength: string
  invalid: string
}) {
  return z.string().superRefine((v, ctx) => {
    if (v.trim().length < 2)
      ctx.addIssue({ code: "custom", message: messages.minLength })
    else if (!HANDLE_RE.test(v))
      ctx.addIssue({ code: "custom", message: messages.invalid })
  })
}

export function createExactLengthCodeSchema(messages: {
  required: string
  length: number
  lengthMessage: string
}) {
  return z.string().superRefine((v, ctx) => {
    const trimmed = v.trim()
    if (!trimmed) ctx.addIssue({ code: "custom", message: messages.required })
    else if (trimmed.length !== messages.length)
      ctx.addIssue({ code: "custom", message: messages.lengthMessage })
  })
}
