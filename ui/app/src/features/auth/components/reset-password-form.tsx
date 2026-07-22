import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useForm } from "@tanstack/react-form"
import { useTranslation } from "react-i18next"
import { IconLoader2 } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { useAuth } from "@/components/auth-provider"
import { toast } from "sonner"

export function ResetPasswordForm() {
  const { t } = useTranslation()
  const { updatePassword } = useAuth()
  const navigate = useNavigate()
  const [formError, setFormError] = useState<string | null>(null)

  const form = useForm({
    defaultValues: { password: "", confirm: "" },
    onSubmit: async ({ value }) => {
      setFormError(null)
      if (value.password !== value.confirm) {
        setFormError(t("auth.resetPassword.passwordMismatch"))
        return
      }
      try {
        await updatePassword(value.password)
        toast.success(t("auth.resetPassword.success"))
        await navigate({ to: "/login" })
      } catch (err) {
        const msg =
          err instanceof Error ? err.message : t("auth.resetPassword.failed")
        setFormError(msg)
      }
    },
  })

  return (
    <div className="flex flex-col gap-8">
      <div>
        <h1 className="text-2xl font-extrabold text-foreground">
          {t("auth.resetPassword.title")}
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          {t("auth.resetPassword.subtitle")}
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
          name="password"
          validators={{
            onChange: ({ value }) => {
              if (!value) return t("auth.resetPassword.passwordRequired")
              if (value.length < 8)
                return t("auth.resetPassword.passwordMinLength")
              return undefined
            },
          }}
        >
          {(field) => (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="password">
                {t("auth.resetPassword.newPassword")}
              </Label>
              <Input
                id="password"
                type="password"
                autoComplete="new-password"
                placeholder="Min. 8 characters"
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

        <form.Field
          name="confirm"
          validators={{
            onChange: ({ value }) =>
              !value ? t("auth.resetPassword.confirmRequired") : undefined,
          }}
        >
          {(field) => (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="confirm">
                {t("auth.resetPassword.confirmPassword")}
              </Label>
              <Input
                id="confirm"
                type="password"
                autoComplete="new-password"
                placeholder={t("auth.resetPassword.confirmPlaceholder")}
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
                ? t("auth.resetPassword.updating")
                : t("auth.resetPassword.updatePassword")}
            </Button>
          )}
        </form.Subscribe>
      </form>
    </div>
  )
}
