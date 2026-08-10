import { useState } from "react"
import { Link } from "@tanstack/react-router"
import { useForm } from "@tanstack/react-form"
import { useTranslation } from "react-i18next"
import { IconEye, IconEyeOff, IconLoader2 } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { useSignIn } from "@/features/auth/hooks/use-sign-in"
import {
  createEmailSchema,
  createPasswordSchema,
  fieldValidator,
} from "@/lib/validation/schemas"

export function LoginForm() {
  const { t } = useTranslation()
  const { signInAndRedirect } = useSignIn()
  const [formError, setFormError] = useState<string | null>(null)
  const [showPassword, setShowPassword] = useState(false)

  const form = useForm({
    defaultValues: { email: "", password: "" },
    onSubmit: async ({ value }) => {
      setFormError(null)
      try {
        await signInAndRedirect(value.email, value.password)
      } catch (err) {
        const msg =
          err instanceof Error
            ? err.message
            : t("auth.login.invalidCredentials")
        setFormError(msg)
      }
    },
  })

  return (
    <div className="flex flex-col gap-8">
      <div>
        <h1 className="text-2xl font-extrabold text-foreground">
          {t("auth.login.title")}
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          {t("auth.login.subtitle")}
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
                required: t("auth.login.emailRequired"),
                invalid: t("auth.login.emailInvalid"),
              })
            ),
          }}
        >
          {(field) => (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="email">{t("auth.login.email")}</Label>
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

        <form.Field
          name="password"
          validators={{
            onChange: fieldValidator(
              createPasswordSchema({
                required: t("auth.login.passwordRequired"),
              })
            ),
          }}
        >
          {(field) => (
            <div className="flex flex-col gap-1.5">
              <div className="flex items-center justify-between">
                <Label htmlFor="password">{t("auth.login.password")}</Label>
                <Link
                  to="/forgot-password"
                  className="text-xs text-muted-foreground transition-colors hover:text-foreground"
                >
                  {t("auth.login.forgotPassword")}
                </Link>
              </div>
              <div className="relative">
                <Input
                  id="password"
                  type={showPassword ? "text" : "password"}
                  autoComplete="current-password"
                  placeholder="••••••••"
                  className="pr-9"
                  value={field.state.value}
                  onBlur={field.handleBlur}
                  onChange={(e) => field.handleChange(e.target.value)}
                />
                <button
                  type="button"
                  onClick={() => setShowPassword((v) => !v)}
                  className="absolute top-1/2 right-1 flex size-7 -translate-y-1/2 items-center justify-center text-muted-foreground hover:text-foreground"
                  aria-label={
                    showPassword
                      ? t("common.hidePassword")
                      : t("common.showPassword")
                  }
                >
                  {showPassword ? (
                    <IconEyeOff className="size-4" />
                  ) : (
                    <IconEye className="size-4" />
                  )}
                </button>
              </div>
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
          <p
            role="alert"
            className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {formError}
          </p>
        )}

        <form.Subscribe selector={(s) => s.isSubmitting}>
          {(isSubmitting) => (
            <Button type="submit" disabled={isSubmitting} className="w-full">
              {isSubmitting ? (
                <IconLoader2 className="mr-2 size-4 animate-spin" />
              ) : null}
              {isSubmitting
                ? t("auth.login.signingIn")
                : t("auth.login.signIn")}
            </Button>
          )}
        </form.Subscribe>
      </form>

      <p className="text-center text-sm text-muted-foreground">
        {t("auth.login.noAccount")}{" "}
        <Link
          to="/register"
          className="font-medium text-foreground underline-offset-4 hover:underline"
        >
          {t("auth.login.createOne")}
        </Link>
      </p>
    </div>
  )
}
