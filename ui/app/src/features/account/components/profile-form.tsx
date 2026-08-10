import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconLoader2 } from "@tabler/icons-react"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import {
  useProfile,
  useUpdateProfile,
  useUpdatePreferences,
} from "@/features/account/hooks"
import { useAuth } from "@/components/auth-provider"
import type { UserProfile } from "@/types/account"
import {
  createOptionalHandleSchema,
  createRequiredTextSchema,
  fieldValidator,
} from "@/lib/validation/schemas"

export function ProfileForm() {
  const { user } = useAuth()
  const { data, isLoading } = useProfile()
  const profile = data?.data

  if (isLoading || !profile) {
    return (
      <Card>
        <CardHeader>
          <Skeleton className="h-5 w-24" />
          <Skeleton className="h-4 w-48" />
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
        </CardContent>
      </Card>
    )
  }

  return <ProfileFormInner profile={profile} email={user?.email ?? ""} />
}

function ProfileFormInner({
  profile,
  email,
}: {
  profile: UserProfile
  email: string
}) {
  const { t } = useTranslation()
  const [serverError, setServerError] = useState<string | null>(null)

  const { mutateAsync: updateProfile, isPending: savingProfile } =
    useUpdateProfile()
  const { mutateAsync: updatePreferences, isPending: savingPreferences } =
    useUpdatePreferences()
  const isPending = savingProfile || savingPreferences

  const form = useForm({
    defaultValues: {
      full_name: profile.full_name ?? "",
      display_name: (profile.preferences?.display_name as string) ?? "",
    },
    onSubmit: async ({ value }) => {
      setServerError(null)
      try {
        await updateProfile({ full_name: value.full_name.trim() })
        await updatePreferences({
          display_name: value.display_name.trim(),
        })
        toast.success(t("account.form.saved"))
      } catch (err: unknown) {
        const msg =
          (err as { status?: { message?: string } })?.status?.message ??
          t("account.form.saveFailed")
        setServerError(msg)
      }
    },
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("account.form.title")}</CardTitle>
        <CardDescription>{t("account.form.description")}</CardDescription>
      </CardHeader>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          void form.handleSubmit()
        }}
      >
        <CardContent className="grid grid-cols-1 gap-5 sm:grid-cols-2">
          <form.Field
            name="full_name"
            validators={{
              onChange: fieldValidator(
                createRequiredTextSchema(t("account.form.nameRequired"))
              ),
            }}
          >
            {(field) => (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="full_name">{t("account.form.fullName")}</Label>
                <Input
                  id="full_name"
                  placeholder={t("account.form.fullNamePlaceholder")}
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
            name="display_name"
            validators={{
              onChange: fieldValidator(
                createOptionalHandleSchema(t("account.form.displayNameInvalid"))
              ),
            }}
          >
            {(field) => (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="display_name">
                  {t("account.form.displayName")}
                </Label>
                <Input
                  id="display_name"
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

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="email">{t("account.form.email")}</Label>
            <Input id="email" value={email} disabled />
            <p className="text-xs text-muted-foreground">
              {t("account.form.emailHint")}
            </p>
          </div>

          {serverError && (
            <p className="col-span-full rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {serverError}
            </p>
          )}
        </CardContent>

        <CardFooter className="mt-4">
          <form.Subscribe selector={(state) => state.isDirty}>
            {(isDirty) => (
              <Button type="submit" disabled={!isDirty || isPending}>
                {isPending && (
                  <IconLoader2
                    data-icon="inline-start"
                    className="animate-spin"
                  />
                )}
                {t("account.form.saveChanges")}
              </Button>
            )}
          </form.Subscribe>
        </CardFooter>
      </form>
    </Card>
  )
}
