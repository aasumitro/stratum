import { useState } from "react"
import { useNavigate, useSearch } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import {
  IconLoader2,
  IconX,
  IconMailX,
  IconUserCheck,
  IconMailQuestion,
  IconUserExclamation,
} from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { useAuth } from "@/components/auth-provider"
import {
  useInvitationPreview,
  useAcceptInvitation,
  useDeclineInvitation,
  useRequestNewInvitation,
} from "@/features/organization/hooks"
import { parseApiError } from "@/lib/api/error"

type State =
  | "loading"
  | "confirm"
  | "expired"
  | "already-member"
  | "invalid"
  | "wrong-account"
  | "no-token"

function Shell({
  icon,
  tone = "muted",
  title,
  description,
  actions,
}: {
  icon: React.ReactNode
  tone?: "muted" | "success" | "destructive" | "primary"
  title: string
  description?: string
  actions: React.ReactNode
}) {
  const toneClass = {
    muted: "bg-muted text-muted-foreground",
    success: "bg-emerald-500/10 text-emerald-500",
    destructive: "bg-destructive/10 text-destructive",
    primary: "bg-primary/10 text-primary",
  }[tone]
  return (
    <div className="flex min-h-[60vh] flex-col items-center justify-center gap-6 text-center">
      <div
        className={`flex size-16 items-center justify-center rounded-full ${toneClass}`}
      >
        {icon}
      </div>
      <div>
        <h1 className="text-xl font-extrabold">{title}</h1>
        {description && (
          <p className="mt-1 max-w-sm text-sm text-muted-foreground">
            {description}
          </p>
        )}
      </div>
      <div className="flex gap-3">{actions}</div>
    </div>
  )
}

// All 5 invitation states resolve to one screen shape: an
// icon, a message, and one forward action — never a dead end.
export function InvitationAcceptPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { user } = useAuth()
  const search = useSearch({ strict: false }) as { token?: string }
  const token = search.token ?? ""
  const [declined, setDeclined] = useState(false)
  const [requestedNew, setRequestedNew] = useState(false)

  const { data, isLoading, error } = useInvitationPreview(token)
  const { mutate: accept, isPending: accepting } = useAcceptInvitation()
  const { mutate: decline, isPending: declining } = useDeclineInvitation()
  const { mutate: requestNew, isPending: requestingNew } =
    useRequestNewInvitation()

  const errCode = (
    error as { status?: { code?: string; details?: Record<string, string> } }
  )?.status?.code
  const errDetails = (
    error as { status?: { details?: Record<string, string> } }
  )?.status?.details

  let state: State = "loading"
  if (!token) state = "no-token"
  else if (declined)
    state = "no-token" // treated as a dead-end-free exit, same visual as no-token
  else if (isLoading) state = "loading"
  else if (error) {
    if (errCode === "INVITATION_EXPIRED") state = "expired"
    else if (errCode === "INVITATION_ALREADY_MEMBER") state = "already-member"
    else if (errCode === "INVITATION_EMAIL_MISMATCH") state = "wrong-account"
    else state = "invalid"
  } else if (data?.data) state = "confirm"

  const organizationId = errDetails?.organization_id

  if (state === "loading") {
    return (
      <Shell
        icon={<IconLoader2 className="size-8 animate-spin" />}
        title={t("organization.invitations.accepting")}
        actions={null}
      />
    )
  }

  if (state === "confirm" && data?.data) {
    const preview = data.data
    return (
      <Shell
        icon={<IconMailQuestion className="size-8" />}
        tone="primary"
        title={t("organization.invitations.confirmTitle", {
          organization: preview.organization_name,
        })}
        description={t("organization.invitations.confirmDesc", {
          inviter: preview.invited_by_email ?? "",
          role: t(`organization.roles.${preview.role}`),
        })}
        actions={
          <>
            <Button
              variant="outline"
              disabled={declining}
              onClick={() =>
                decline(
                  { token },
                  {
                    onSuccess: () => setDeclined(true),
                    onError: (err) =>
                      toast.error(
                        parseApiError(
                          err,
                          t("organization.invitations.declineFailed")
                        )
                      ),
                  }
                )
              }
            >
              {declining && (
                <IconLoader2
                  data-icon="inline-start"
                  className="animate-spin"
                />
              )}
              {t("organization.invitations.decline")}
            </Button>
            <Button
              disabled={accepting}
              onClick={() =>
                accept(
                  { token },
                  {
                    onSuccess: () =>
                      void navigate({
                        to: "/organization/$organizationId",
                        params: { organizationId: preview.organization_id },
                      }),
                  }
                )
              }
            >
              {accepting && (
                <IconLoader2
                  data-icon="inline-start"
                  className="animate-spin"
                />
              )}
              {t("organization.invitations.acceptAction")}
            </Button>
          </>
        }
      />
    )
  }

  if (state === "expired") {
    return (
      <Shell
        icon={<IconMailX className="size-8" />}
        tone="destructive"
        title={t("organization.invitations.expiredTitle")}
        description={t("organization.invitations.expiredDesc")}
        actions={
          <Button
            variant="outline"
            disabled={requestingNew || requestedNew}
            onClick={() =>
              requestNew({ token }, { onSuccess: () => setRequestedNew(true) })
            }
          >
            {requestingNew && (
              <IconLoader2 data-icon="inline-start" className="animate-spin" />
            )}
            {requestedNew
              ? t("organization.invitations.newInviteRequested")
              : t("organization.invitations.requestNewInvite")}
          </Button>
        }
      />
    )
  }

  if (state === "already-member") {
    return (
      <Shell
        icon={<IconUserCheck className="size-8" />}
        tone="success"
        title={t("organization.invitations.alreadyMemberTitle")}
        actions={
          <Button
            onClick={() =>
              organizationId
                ? void navigate({
                    to: "/organization/$organizationId",
                    params: { organizationId },
                  })
                : void navigate({ to: "/organizations" })
            }
          >
            {t("organization.invitations.goToOrganization")}
          </Button>
        }
      />
    )
  }

  if (state === "wrong-account") {
    return (
      <Shell
        icon={<IconUserExclamation className="size-8" />}
        tone="destructive"
        title={t("organization.invitations.wrongAccountTitle", {
          invitedEmail: errDetails?.invited_email ?? "",
          currentEmail: user?.email ?? "",
        })}
        description={t("organization.invitations.wrongAccountDesc")}
        actions={
          <Button
            variant="outline"
            onClick={() => void navigate({ to: "/login" })}
          >
            {t("organization.invitations.switchAccount")}
          </Button>
        }
      />
    )
  }

  if (state === "no-token" && declined) {
    return (
      <Shell
        icon={<IconX className="size-8" />}
        title={t("organization.invitations.declinedTitle")}
        actions={
          <Button
            variant="outline"
            onClick={() => void navigate({ to: "/organizations" })}
          >
            {t("organization.invitations.goToOrganizations")}
          </Button>
        }
      />
    )
  }

  if (state === "no-token") {
    return (
      <Shell
        icon={<IconX className="size-8" />}
        title={t("organization.invitations.invalidLink")}
        description={t("organization.invitations.invalidLinkDesc")}
        actions={
          <Button
            variant="outline"
            onClick={() => void navigate({ to: "/organizations" })}
          >
            {t("organization.invitations.goToOrganizations")}
          </Button>
        }
      />
    )
  }

  // "invalid" — not found / revoked / mistyped.
  return (
    <Shell
      icon={<IconX className="size-8" />}
      tone="destructive"
      title={t("organization.invitations.invalidTitle")}
      description={t("organization.invitations.invalidDesc")}
      actions={
        <>
          <Button
            variant="outline"
            onClick={() => void navigate({ to: "/join" })}
          >
            {t("organization.invitations.enterCodeManually")}
          </Button>
          <Button onClick={() => void navigate({ to: "/organizations" })}>
            {t("organization.invitations.goToOrganizations")}
          </Button>
        </>
      }
    />
  )
}
