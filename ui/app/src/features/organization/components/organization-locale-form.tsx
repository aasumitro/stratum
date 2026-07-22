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
  useOrganization,
  useUpdateOrganizationSettings,
} from "@/features/organization/hooks"

interface Props {
  organizationId: string
  isOwner: boolean
}

export function OrganizationLocaleForm({ organizationId, isOwner }: Props) {
  const { t } = useTranslation()
  const { data, isLoading } = useOrganization(organizationId)
  const { mutate: updateSettings, isPending } =
    useUpdateOrganizationSettings(organizationId)

  const organization = data?.data

  const form = useForm({
    defaultValues: {
      timezone: organization?.timezone ?? "",
      locale: organization?.locale ?? "",
    },
    onSubmit: ({ value }) =>
      updateSettings({
        timezone: value.timezone.trim(),
        locale: value.locale.trim(),
      }),
  })

  if (isLoading) {
    return (
      <Card>
        <CardContent className="flex flex-col gap-4 pt-6">
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
        </CardContent>
      </Card>
    )
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("organization.settings.localeTitle")}</CardTitle>
        <CardDescription>
          {t("organization.settings.localeDescription")}
        </CardDescription>
      </CardHeader>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          void form.handleSubmit()
        }}
      >
        <CardContent className="flex flex-col gap-4">
          <form.Field name="timezone">
            {(field) => (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="ws-tz">
                  {t("organization.settings.timezoneLabel")}
                </Label>
                <Input
                  id="ws-tz"
                  placeholder="Asia/Jakarta"
                  value={field.state.value}
                  disabled={!isOwner}
                  onBlur={field.handleBlur}
                  onChange={(e) => field.handleChange(e.target.value)}
                />
              </div>
            )}
          </form.Field>

          <form.Field name="locale">
            {(field) => (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="ws-locale">
                  {t("organization.settings.localeLabel")}
                </Label>
                <Input
                  id="ws-locale"
                  placeholder="en"
                  value={field.state.value}
                  disabled={!isOwner}
                  onBlur={field.handleBlur}
                  onChange={(e) => field.handleChange(e.target.value)}
                />
              </div>
            )}
          </form.Field>
        </CardContent>
        {isOwner && (
          <CardFooter className="mt-4">
            <Button type="submit" disabled={isPending}>
              {isPending && (
                <IconLoader2
                  data-icon="inline-start"
                  className="animate-spin"
                />
              )}
              {t("common.save")}
            </Button>
          </CardFooter>
        )}
      </form>
    </Card>
  )
}
