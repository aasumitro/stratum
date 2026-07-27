import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconCopy, IconRefresh } from "@tabler/icons-react"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import {
  useOrganization,
  useGenerateInviteCode,
  useToggleInviteCode,
} from "@/features/organization/hooks"

interface Props {
  organizationId: string
  isOwner: boolean
  /** true when embedded inside another chrome (e.g. the invite dialog's
   * "By code" tab) — skips the Card wrapper/title so it doesn't nest. */
  bare?: boolean
}

export function InviteCodeSection({
  organizationId,
  isOwner,
  bare = false,
}: Props) {
  const { t } = useTranslation()
  const { data } = useOrganization(organizationId)
  const organization = data?.data

  const { mutate: generate, isPending: generating } =
    useGenerateInviteCode(organizationId)
  const { mutate: toggle, isPending: toggling } =
    useToggleInviteCode(organizationId)

  function copyCode() {
    if (organization?.invite_code) {
      void navigator.clipboard.writeText(organization.invite_code)
      toast.success(t("organization.inviteCode.copied"))
    }
  }

  if (!isOwner) return null

  const body = (
    <div className="flex flex-col gap-3">
      {organization?.invite_code ? (
        <div className="flex items-center gap-2">
          <code className="flex-1 rounded-lg bg-muted px-3 py-1.5 font-mono text-sm">
            {organization.invite_code_enabled
              ? organization.invite_code
              : "•••••••"}
          </code>
          <Button
            variant="outline"
            size="sm"
            onClick={copyCode}
            disabled={!organization.invite_code_enabled}
          >
            <IconCopy data-icon="inline-start" />
            {t("organization.inviteCode.copy")}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => generate()}
            disabled={generating}
          >
            <IconRefresh data-icon="inline-start" />
            {t("organization.inviteCode.regenerate")}
          </Button>
        </div>
      ) : (
        <div className="flex items-center justify-between">
          <p className="text-sm text-muted-foreground">
            {t("organization.inviteCode.noCode")}
          </p>
          <Button size="sm" onClick={() => generate()} disabled={generating}>
            <IconRefresh data-icon="inline-start" />
            {t("organization.inviteCode.generate")}
          </Button>
        </div>
      )}

      {organization?.invite_code && (
        <div className="flex flex-col gap-1">
          <div className="flex items-center gap-2 text-sm">
            <span className="text-muted-foreground">
              {t("organization.inviteCode.expiresJoinsAsMember")}
            </span>
            <Switch
              checked={organization.invite_code_enabled}
              onCheckedChange={(v) => toggle({ enabled: v })}
              disabled={toggling}
              aria-label={t("organization.inviteCode.title")}
            />
            <span className="text-xs text-muted-foreground">
              {organization.invite_code_enabled
                ? t("organization.inviteCode.active")
                : t("organization.inviteCode.disabled")}
            </span>
          </div>
          {bare && (
            <p className="text-[13px] text-muted-foreground mt-3 leading-relaxed">
              {t("organization.inviteCode.description")}
            </p>
          )}
        </div>
      )}
    </div>
  )

  if (bare) return body

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("organization.inviteCode.title")}</CardTitle>
        <CardDescription>
          {t("organization.inviteCode.description")}
        </CardDescription>
      </CardHeader>
      <CardContent>{body}</CardContent>
    </Card>
  )
}
