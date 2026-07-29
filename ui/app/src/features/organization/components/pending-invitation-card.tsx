import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconLoader2 } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { parseApiError } from "@/lib/api/error"
import {
  useAcceptInvitation,
  useDeclineInvitation,
} from "@/features/organization/hooks/use-invitations"
import type { MyInvitation } from "@/types/organization"

export function PendingInvitationCard({
  invitation,
  disabled,
  onAccepted,
  onDeclined,
}: {
  invitation: MyInvitation
  disabled: boolean
  onAccepted: () => void
  onDeclined: () => void
}) {
  const { t } = useTranslation()
  const { mutate: accept, isPending } = useAcceptInvitation()
  const { mutate: decline, isPending: declining } = useDeclineInvitation()

  return (
    <div className="rounded-xl border border-primary/30 bg-primary/5 px-3 py-2.5">
      <div className="flex items-center gap-3">
        <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-xs font-semibold text-primary">
          {invitation.organization_name.charAt(0).toUpperCase()}
        </div>
        <div className="min-w-0 flex-1">
          <p className="text-sm font-semibold">
            {t("onboarding.organization.pendingInvitationTitle", {
              count: 1,
            })}
          </p>
          <p className="truncate text-xs text-primary">
            {t("onboarding.organization.pendingInvitationDesc", {
              organization: invitation.organization_name,
              inviter: invitation.invited_by_email ?? "",
              role: t(`organization.roles.${invitation.role}`),
            })}
          </p>
        </div>
        <Button
          size="sm"
          disabled={isPending || declining || disabled}
          onClick={() =>
            accept(
              { token: invitation.token },
              {
                onSuccess: onAccepted,
                onError: (err) =>
                  toast.error(
                    parseApiError(
                      err,
                      t("onboarding.organization.acceptFailed")
                    )
                  ),
              }
            )
          }
        >
          {isPending && (
            <IconLoader2 data-icon="inline-start" className="animate-spin" />
          )}
          {t("onboarding.organization.accept")}
        </Button>
        <button
          type="button"
          disabled={isPending || declining || disabled}
          className="text-xs text-primary underline disabled:opacity-50"
          onClick={() =>
            decline(
              { token: invitation.token },
              {
                onSuccess: onDeclined,
                onError: (err) =>
                  toast.error(
                    parseApiError(
                      err,
                      t("onboarding.organization.declineFailed")
                    )
                  ),
              }
            )
          }
        >
          {t("onboarding.organization.decline")}
        </button>
      </div>
    </div>
  )
}
