import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useForm } from "@tanstack/react-form"
import { IconLoader2 } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useHTTPActionPost } from "@/lib/api/action"
import { useInviteCodePreview } from "@/features/organization/hooks"
import { API } from "@/lib/api/path"
import { parseApiError } from "@/lib/api/error"
import type { Organization } from "@/types/organization"

export function JoinOrganizationForm({
  onJoined,
}: {
  onJoined: (org: Organization) => void
}) {
  const { t } = useTranslation()
  const [checkedCode, setCheckedCode] = useState<string | null>(null)
  const [serverError, setServerError] = useState<string | null>(null)

  const {
    data: preview,
    isLoading: checking,
    error: previewError,
    refetch: recheck,
  } = useInviteCodePreview(checkedCode ?? "")

  const { mutate: join, isPending: joining } = useHTTPActionPost<
    Organization,
    { code: string }
  >({
    url: API.organizations("join"),
    options: {
      onSuccess: (res) => {
        if (res.data) onJoined(res.data)
      },
      onError: (err) =>
        setServerError(
          parseApiError(err, t("onboarding.organization.joinFailed"))
        ),
    },
  })

  const form = useForm({
    defaultValues: { code: "" },
    onSubmit: ({ value }) => {
      setServerError(null)
      const trimmed = value.code.trim()
      if (trimmed === checkedCode) void recheck()
      else setCheckedCode(trimmed)
    },
  })

  // Preview rejects a code resolving to an organization the caller already
  // belongs to (JOIN_ALREADY_MEMBER) instead of letting the check step
  // succeed and only failing on confirm — show that state instead of the
  // generic error text, with no confirm button since there's nothing to
  // confirm.
  const alreadyMember =
    checkedCode && previewError?.status?.code === "JOIN_ALREADY_MEMBER"

  if (checkedCode && preview?.data) {
    const org = preview.data
    const owner = org.owner_name || org.owner_email
    return (
      <div className="rounded-xl border p-3">
        <div className="flex items-center gap-3">
          <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-xs font-semibold text-primary">
            {org.organization_name.charAt(0).toUpperCase()}
          </div>
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-semibold">
              {org.organization_name}
            </p>
            {owner && (
              <p className="truncate text-xs text-muted-foreground">
                {t("onboarding.organization.ownedBy", { owner })}
              </p>
            )}
          </div>
        </div>
        {serverError && (
          <p role="alert" className="mt-2 text-xs text-destructive">
            {serverError}
          </p>
        )}
        <div className="mt-3 flex justify-end gap-2">
          <Button
            type="button"
            variant="outline"
            disabled={joining}
            onClick={() => {
              setServerError(null)
              setCheckedCode(null)
            }}
          >
            {t("onboarding.organization.changeCode")}
          </Button>
          <Button
            type="button"
            disabled={joining}
            onClick={() => join({ code: checkedCode })}
          >
            {joining && <IconLoader2 className="mr-2 size-4 animate-spin" />}
            {t("onboarding.organization.confirmJoin")}
          </Button>
        </div>
      </div>
    )
  }

  if (alreadyMember) {
    return (
      <div className="rounded-xl border p-3">
        <p className="text-sm text-muted-foreground">
          {t("organization.inviteCode.alreadyMember")}
        </p>
        <div className="mt-3 flex justify-end">
          <Button
            type="button"
            variant="outline"
            onClick={() => setCheckedCode(null)}
          >
            {t("onboarding.organization.changeCode")}
          </Button>
        </div>
      </div>
    )
  }

  return (
    <div className="rounded-xl border p-3">
      <p className="text-sm font-semibold">
        {t("onboarding.organization.joinCardTitle")}
      </p>
      <p className="text-xs text-muted-foreground">
        {t("onboarding.organization.joinCardSubtitle")}
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          void form.handleSubmit()
        }}
        className="mt-3 flex flex-col gap-2"
      >
        <div className="flex gap-2">
          <form.Field
            name="code"
            validators={{
              onChange: ({ value }) => {
                if (!value.trim())
                  return t("onboarding.organization.codeRequired")
                if (value.trim().length !== 8)
                  return t("onboarding.organization.codeLength")
                return undefined
              },
            }}
          >
            {(field) => (
              <Input
                aria-label={t("onboarding.organization.inviteCode")}
                placeholder={t("onboarding.organization.inviteCodePlaceholder")}
                className="flex-1"
                value={field.state.value}
                onBlur={field.handleBlur}
                onChange={(e) => field.handleChange(e.target.value)}
              />
            )}
          </form.Field>
          <form.Subscribe selector={(s) => s.values.code.trim().length === 8}>
            {(codeComplete) => (
              <Button type="submit" disabled={!codeComplete || checking}>
                {checking && (
                  <IconLoader2 className="mr-2 size-4 animate-spin" />
                )}
                {t("onboarding.organization.checkCode")}
              </Button>
            )}
          </form.Subscribe>
        </div>
        {(serverError || previewError) && (
          <p role="alert" className="text-xs text-destructive">
            {serverError ??
              parseApiError(
                previewError,
                t("onboarding.organization.joinFailed")
              )}
          </p>
        )}
      </form>
    </div>
  )
}
