import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { useOrganization } from "@/features/organization/hooks/use-organization"
import { useOrganizationMembers } from "@/features/organization/hooks/use-members"
import { useUpdateOrganizationSettings } from "@/features/organization/hooks/use-settings"
import { sessionIsAAL2 } from "@/lib/auth/mfa"

interface Props {
  organizationId: string
  isOwner: boolean
}

// Owner-only switch for the organization-wide MFA policy. Turning it on needs
// this session to be aal2 (the API refuses otherwise) and is confirmed with
// the number of active members it would shut out.
export function OrganizationRequireMfaForm({ organizationId, isOwner }: Props) {
  const { t } = useTranslation()
  const { data } = useOrganization(organizationId)
  const { data: membersData } = useOrganizationMembers(organizationId)
  const { mutate: updateSettings, isPending } =
    useUpdateOrganizationSettings(organizationId)
  const [confirmOpen, setConfirmOpen] = useState(false)
  // undefined while Supabase answers, so no hint flashes for an aal2 owner.
  const [aal2, setAal2] = useState<boolean | undefined>(undefined)

  useEffect(() => {
    void sessionIsAAL2().then(setAal2)
  }, [])

  const organization = data?.data
  const enabled = organization?.settings?.require_mfa === true
  const withoutMfa = (membersData?.data ?? []).filter(
    (m) => m.status === "active" && m.mfa_enabled === false
  ).length

  function save(value: boolean) {
    updateSettings(
      {
        timezone: organization?.timezone ?? "",
        locale: organization?.locale ?? "",
        require_mfa: value,
      },
      { onSuccess: () => setConfirmOpen(false) }
    )
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("organization.settings.requireMfaTitle")}</CardTitle>
        <CardDescription>
          {t("organization.settings.requireMfaDescription")}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        <div className="flex items-center justify-between gap-4">
          <Label htmlFor="ws-require-mfa">
            {t("organization.settings.requireMfaLabel")}
          </Label>
          <Switch
            id="ws-require-mfa"
            checked={enabled}
            disabled={
              !isOwner ||
              isPending ||
              !organization ||
              // Turning it on needs the members (for the confirm count) and an
              // aal2 session; turning it off needs neither.
              (!enabled && (!membersData || aal2 !== true))
            }
            onCheckedChange={(checked) =>
              checked ? setConfirmOpen(true) : save(false)
            }
          />
        </div>
        {isOwner && !enabled && aal2 === false && (
          <p className="text-xs text-muted-foreground">
            {t("organization.settings.requireMfaNeedsAal2")}
          </p>
        )}
      </CardContent>

      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("organization.settings.requireMfaConfirmTitle")}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t("organization.settings.requireMfaConfirmDescription", {
                count: withoutMfa,
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction disabled={isPending} onClick={() => save(true)}>
              {t("organization.settings.requireMfaConfirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  )
}
