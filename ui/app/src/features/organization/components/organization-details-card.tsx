import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { useTranslation } from "react-i18next"
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
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogHeader,
  AlertDialogFooter,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { UnsavedChangesGuard } from "@/components/shared/unsaved-changes-guard"
import {
  useOrganization,
  useUpdateOrganization,
} from "@/features/organization/hooks/use-organization"
import { useUpdateOrganizationSettings } from "@/features/organization/hooks/use-settings"
import { slugify } from "@/lib/format"

const TIMEZONES = Intl.supportedValuesOf("timeZone")

const LANGUAGES = [
  { value: "en", key: "language.en" },
  { value: "id", key: "language.id" },
] as const

interface Props {
  organizationId: string
  isOwner: boolean
}

interface FormValues {
  name: string
  slug: string
  timezone: string
  locale: string
}

/**
 * Name/slug and locale/timezone in one card, one form, one Save — these
 * used to be two near-identical cards each with their own button, which
 * made no sense once they're visually one thing. Still two mutations under
 * the hood (`useUpdateOrganization` for name/slug,
 * `useUpdateOrganizationSettings` for timezone/locale — different backend
 * fields), fired together on submit; the slug-change confirm dialog still
 * gates the whole submit, not just its own field.
 */
export function OrganizationDetailsCard({ organizationId, isOwner }: Props) {
  const { t } = useTranslation()
  const { data, isLoading } = useOrganization(organizationId)
  const { mutateAsync: updateBasic, isPending: savingBasic } =
    useUpdateOrganization(organizationId)
  const { mutateAsync: updateSettings, isPending: savingSettings } =
    useUpdateOrganizationSettings(organizationId)
  const [pendingSlugChange, setPendingSlugChange] = useState<FormValues | null>(
    null
  )

  const organization = data?.data
  const saving = savingBasic || savingSettings

  async function commit(value: FormValues) {
    if (!organization) return

    const basicChanged =
      value.name.trim() !== organization.name ||
      value.slug.trim() !== organization.slug

    const settingsChanged =
      value.timezone.trim() !== (organization.timezone ?? "") ||
      value.locale.trim() !== (organization.locale ?? "")

    const promises = []

    if (basicChanged) {
      promises.push(
        updateBasic({ name: value.name.trim(), slug: value.slug.trim() })
      )
    }

    if (settingsChanged) {
      promises.push(
        updateSettings({
          timezone: value.timezone.trim(),
          locale: value.locale.trim(),
        })
      )
    }

    if (promises.length === 0) {
      form.reset(value)
      return
    }

    try {
      await Promise.all(promises)
      // Update form's baseline so it's no longer dirty
      form.reset(value)
    } catch {
      // Let global error handlers deal with toasts
    }
  }

  const form = useForm({
    defaultValues: {
      name: organization?.name ?? "",
      slug: organization?.slug ?? "",
      timezone: organization?.timezone ?? "",
      locale: organization?.locale ?? "",
    },
    onSubmit: async ({ value }) => {
      // Slug changes break existing invite/webhook links, so they get their
      // own confirm — the whole save waits on it, not just the slug field.
      if (organization && value.slug.trim() !== organization.slug) {
        setPendingSlugChange(value)
        return
      }
      await commit(value)
    },
  })

  if (isLoading) {
    return (
      <Card>
        <CardContent className="flex flex-col gap-4 pt-6">
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
        </CardContent>
      </Card>
    )
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("organization.settings.detailsTitle")}</CardTitle>
        <CardDescription>
          {t("organization.settings.detailsDescription")}
        </CardDescription>
      </CardHeader>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          void form.handleSubmit()
        }}
      >
        <CardContent className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <form.Field
            name="name"
            validators={{
              onChange: ({ value }) =>
                !value.trim()
                  ? t("organization.settings.nameRequired")
                  : undefined,
            }}
          >
            {(field) => (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="ws-name">
                  {t("organization.settings.nameLabel")}
                </Label>
                <Input
                  id="ws-name"
                  value={field.state.value}
                  disabled={!isOwner}
                  onBlur={field.handleBlur}
                  onChange={(e) => field.handleChange(e.target.value)}
                />
                {field.state.meta.isTouched && field.state.meta.errors[0] && (
                  <p className="text-xs text-destructive">
                    {field.state.meta.errors[0]}
                  </p>
                )}
              </div>
            )}
          </form.Field>

          <form.Field
            name="slug"
            validators={{
              onChange: ({ value }) =>
                !value.trim()
                  ? t("organization.settings.slugRequired")
                  : undefined,
            }}
          >
            {(field) => (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="ws-slug">
                  {t("organization.settings.slugLabel")}
                </Label>
                <Input
                  id="ws-slug"
                  value={field.state.value}
                  disabled={!isOwner}
                  onBlur={field.handleBlur}
                  onChange={(e) => field.handleChange(slugify(e.target.value))}
                />
              </div>
            )}
          </form.Field>

          <form.Field name="timezone">
            {(field) => (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="ws-tz">
                  {t("organization.settings.timezoneLabel")}
                </Label>
                <Select
                  value={field.state.value}
                  onValueChange={(v) => v && field.handleChange(v)}
                  disabled={!isOwner}
                >
                  <SelectTrigger id="ws-tz" className="w-full">
                    <SelectValue placeholder={t("account.timezone")}>
                      {field.state.value
                        ? field.state.value.replace(/_/g, " ")
                        : undefined}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent
                    className="max-h-72"
                    alignItemWithTrigger={false}
                  >
                    {TIMEZONES.map((tz) => (
                      <SelectItem key={tz} value={tz}>
                        {tz.replace(/_/g, " ")}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            )}
          </form.Field>

          <form.Field name="locale">
            {(field) => (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="ws-locale">
                  {t("organization.settings.localeLabel")}
                </Label>
                <Select
                  value={field.state.value}
                  onValueChange={(v) => v && field.handleChange(v)}
                  disabled={!isOwner}
                >
                  <SelectTrigger id="ws-locale" className="w-full">
                    <SelectValue placeholder={t("settings.language.title")}>
                      {field.state.value
                        ? t(
                            LANGUAGES.find((l) => l.value === field.state.value)
                              ?.key ?? "language.en"
                          )
                        : undefined}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {LANGUAGES.map(({ value, key }) => (
                      <SelectItem key={value} value={value}>
                        {t(key)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            )}
          </form.Field>
        </CardContent>

        <form.Subscribe selector={(s) => s.isDirty}>
          {(isDirty) =>
            isOwner && (
              <CardFooter className="mt-4">
                <Button type="submit" disabled={saving || !isDirty}>
                  {saving && (
                    <IconLoader2
                      data-icon="inline-start"
                      className="animate-spin"
                    />
                  )}
                  {t("common.saveChanges")}
                </Button>
              </CardFooter>
            )
          }
        </form.Subscribe>
      </form>

      {isOwner && (
        <form.Subscribe selector={(s) => s.isDirty}>
          {(isDirty) => <UnsavedChangesGuard isDirty={isDirty} />}
        </form.Subscribe>
      )}

      <AlertDialog
        open={!!pendingSlugChange}
        onOpenChange={(open) => !open && setPendingSlugChange(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("organization.settings.slugChangeTitle")}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t("organization.settings.slugChangeDescription")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (pendingSlugChange) commit(pendingSlugChange)
                setPendingSlugChange(null)
              }}
            >
              {t("organization.settings.slugChangeConfirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  )
}
