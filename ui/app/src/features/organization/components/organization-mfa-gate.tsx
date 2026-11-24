import { useNavigate } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { IconShieldLock } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { ConfirmationDialog } from "@/components/shared/confirmation-dialog"
import { useLeaveOrganization } from "@/features/organization/hooks/use-members"
import { useActiveOrganization } from "@/hooks/use-active-organization"

// Shown instead of the organization when it requires MFA and this session has
// no verified second factor. The member keeps the organization: setting up
// MFA lets them in, and leaving stays possible.
export function OrganizationMfaGate({
  organizationId,
}: {
  organizationId: string
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { mutate: leave, isPending: leaving } =
    useLeaveOrganization(organizationId)
  // An owner cannot leave (the API refuses it); they set up MFA or transfer.
  const { role } = useActiveOrganization()

  return (
    <div className="flex flex-col items-center justify-center gap-4 py-16 text-center">
      <div className="flex size-14 items-center justify-center rounded-2xl bg-primary/10">
        <IconShieldLock className="size-7 text-primary" />
      </div>
      <h2 className="text-lg font-semibold">
        {t("organization.mfaGate.title")}
      </h2>
      <p className="max-w-sm text-sm text-muted-foreground">
        {t("organization.mfaGate.description")}
      </p>
      <div className="flex flex-wrap items-center justify-center gap-2">
        <Button
          onClick={() =>
            void navigate({ to: "/account", search: { tab: "security" } })
          }
        >
          {t("organization.mfaGate.setUp")}
        </Button>
        {role !== undefined && role !== "owner" && (
          <ConfirmationDialog
            render={<Button variant="ghost" disabled={leaving} />}
            title={t("organization.members.leaveTitle")}
            description={t("organization.members.leaveDescription")}
            confirmLabel={t("organization.members.leave")}
            pending={leaving}
            onConfirm={() =>
              leave(undefined, {
                onSuccess: () => void navigate({ to: "/organizations" }),
              })
            }
          >
            {t("organization.members.leave")}
          </ConfirmationDialog>
        )}
      </div>
    </div>
  )
}
