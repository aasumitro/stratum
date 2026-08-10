import { useState } from "react"
import { Link } from "@tanstack/react-router"
import { useForm } from "@tanstack/react-form"
import { useTranslation } from "react-i18next"
import { IconLoader2, IconArrowLeft } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { useAuth } from "@/components/auth-provider"
import { createEmailSchema, fieldValidator } from "@/lib/validation/schemas"

export function ForgotPasswordForm() {
  const { t } = useTranslation()
  const { resetPassword } = useAuth()
  const [sent, setSent] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)

  const form = useForm({
    defaultValues: { email: "" },
    onSubmit: async ({ value }) => {
      setFormError(null)
      try {
        await resetPassword(value.email)
        setSent(true)
      } catch (err) {
        const msg =
          err instanceof Error ? err.message : t("auth.forgotPassword.failed")
        setFormError(msg)
      }
    },
  })

  if (sent) {
    return (
      <div className="flex flex-col gap-6">
        <div>
          <h1 className="text-2xl font-extrabold text-foreground">
            {t("auth.forgotPassword.sentTitle")}
          </h1>
          <p className="mt-2 text-sm text-muted-foreground">
            {t("auth.forgotPassword.sentSubtitle", {
              email: form.getFieldValue("email"),
            })}
          </p>
        </div>
        <Link
          to="/login"
          className="flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
        >
          <IconArrowLeft className="size-4" />
          {t("auth.forgotPassword.backToSignIn")}
        </Link>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-8">
      <div>
        <h1 className="text-2xl font-extrabold text-foreground">
          {t("auth.forgotPassword.title")}
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          {t("auth.forgotPassword.subtitle")}
        </p>
      </div>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          void form.handleSubmit()
        }}
        className="flex flex-col gap-5"
      >
        <form.Field
          name="email"
          validators={{
            onChange: fieldValidator(
              createEmailSchema({
                required: t("auth.forgotPassword.emailRequired"),
                invalid: t("auth.forgotPassword.emailInvalid"),
              })
            ),
          }}
        >
          {(field) => (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="email">{t("auth.forgotPassword.email")}</Label>
              <Input
                id="email"
                type="email"
                autoComplete="email"
                placeholder="you@company.com"
                value={field.state.value}
                onBlur={field.handleBlur}
                onChange={(e) => field.handleChange(e.target.value)}
              />
              {field.state.meta.isTouched &&
                field.state.meta.errors.length > 0 && (
                  <p className="text-xs text-destructive">
                    {field.state.meta.errors[0]}
                  </p>
                )}
            </div>
          )}
        </form.Field>

        {formError && (
          <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {formError}
          </p>
        )}

        <form.Subscribe selector={(s) => s.isSubmitting}>
          {(isSubmitting) => (
            <Button type="submit" disabled={isSubmitting} className="w-full">
              {isSubmitting && (
                <IconLoader2 className="mr-2 size-4 animate-spin" />
              )}
              {isSubmitting
                ? t("auth.forgotPassword.sending")
                : t("auth.forgotPassword.sendResetLink")}
            </Button>
          )}
        </form.Subscribe>
      </form>

      <Link
        to="/login"
        className="flex items-center justify-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
      >
        <IconArrowLeft className="size-4" />
        {t("auth.forgotPassword.backToSignIn")}
      </Link>
    </div>
  )
}
