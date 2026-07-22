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
} from "@/features/organization/hooks"
import { slugify } from "@/lib/format"

interface Props {
  organizationId: string
  isOwner: boolean
}

export function OrganizationBasicForm({ organizationId, isOwner }: Props) {
  const { t } = useTranslation()
  const { data, isLoading } = useOrganization(organizationId)
  const { mutate: updateBasic, isPending } =
    useUpdateOrganization(organizationId)
  const [pendingSlugChange, setPendingSlugChange] = useState<{
    name: string
    slug: string
  } | null>(null)

  const organization = data?.data

  const form = useForm({
    defaultValues: {
      name: organization?.name ?? "",
      slug: organization?.slug ?? "",
    },
    onSubmit: ({ value }) => {
      const name = value.name.trim()
      const slug = value.slug.trim()
      // Slug changes break existing invite/webhook links, so it gets
      // its own confirm instead of saving silently alongside the name.
      if (organization && slug !== organization.slug) {
        setPendingSlugChange({ name, slug })
        return
      }
      updateBasic({ name, slug })
    },
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
        <CardContent className="flex flex-col gap-4">
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
                  onChange={(e) => {
                    field.handleChange(e.target.value)
                    form.setFieldValue("slug", slugify(e.target.value))
                  }}
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
                if (pendingSlugChange) updateBasic(pendingSlugChange)
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
