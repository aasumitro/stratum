import { useState } from "react"
import { Link } from "@tanstack/react-router"
import { useForm } from "@tanstack/react-form"
import { useTranslation } from "react-i18next"
import {
  IconEye,
  IconEyeOff,
  IconLoader2,
  IconMailCheck,
} from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { useSignUp } from "@/features/auth/hooks/use-sign-up"

export function RegisterForm() {
  const { t } = useTranslation()
  const { signUpAndRedirect } = useSignUp()
  const [formError, setFormError] = useState<string | null>(null)
  const [emailSent, setEmailSent] = useState(false)
  const [sentTo, setSentTo] = useState("")
  const [showPassword, setShowPassword] = useState(false)

  const form = useForm({
    defaultValues: { email: "", password: "" },
    onSubmit: async ({ value }) => {
      setFormError(null)
      try {
        const needsConfirmation = await signUpAndRedirect(
          value.email,
          value.password
        )
        if (needsConfirmation) {
          setSentTo(value.email)
          setEmailSent(true)
        }
      } catch (err) {
        const msg =
          err instanceof Error ? err.message : t("auth.register.failed")
        setFormError(msg)
      }
    },
  })

  if (emailSent) {
    return (
      <div className="flex flex-col items-center gap-6 text-center">
        <div className="flex size-16 items-center justify-center rounded-full bg-primary/10">
          <IconMailCheck className="size-8 text-primary" />
        </div>
        <div>
          <h1 className="text-2xl font-extrabold text-foreground">
            {t("auth.register.checkEmailTitle")}
          </h1>
          <p className="mt-2 text-sm text-muted-foreground">
            {t("auth.register.checkEmailDesc", { email: sentTo })}
          </p>
        </div>
        <p className="text-sm text-muted-foreground">
          {t("auth.register.haveAccount")}{" "}
          <Link
            to="/login"
            className="font-medium text-foreground underline-offset-4 hover:underline"
          >
            {t("auth.register.signIn")}
          </Link>
        </p>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-8">
      <div>
        <h1 className="text-2xl font-extrabold text-foreground">
          {t("auth.register.title")}
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          {t("auth.register.subtitle")}
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
            onChange: ({ value }) => {
              if (!value) return t("auth.register.emailRequired")
              if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value))
                return t("auth.register.emailInvalid")
              return undefined
            },
          }}
        >
          {(field) => (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="email">{t("auth.register.workEmail")}</Label>
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
            onChange: ({ value }) => {
              if (!value) return t("auth.register.passwordRequired")
              if (value.length < 8) return t("auth.register.passwordMinLength")
              return undefined
            },
          }}
        >
          {(field) => (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="password">{t("auth.register.password")}</Label>
              <div className="relative">
                <Input
                  id="password"
                  type={showPassword ? "text" : "password"}
                  autoComplete="new-password"
                  placeholder={t("auth.register.passwordPlaceholder")}
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
                ? t("auth.register.creatingAccount")
                : t("auth.register.createAccount")}
            </Button>
          )}
        </form.Subscribe>

        <p className="text-center text-xs text-muted-foreground">
          {t("auth.register.terms")}
        </p>
      </form>

      <p className="text-center text-sm text-muted-foreground">
        {t("auth.register.haveAccount")}{" "}
        <Link
          to="/login"
          className="font-medium text-foreground underline-offset-4 hover:underline"
        >
          {t("auth.register.signIn")}
        </Link>
      </p>
    </div>
  )
}
