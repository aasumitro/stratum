import { useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { useHTTPQuery } from "@/lib/api/query"
import {
  useHTTPActionPost,
  useHTTPActionPatch,
  useHTTPActionDelete,
} from "@/lib/api/action"
import { queryKeys } from "@/lib/api/keys"
import { API } from "@/lib/api/path"
import { capture } from "@/lib/analytics"
import type {
  Organization,
  OrganizationView,
  Invitation,
  MyInvitation,
  InvitationPreview,
  InviteCodePreview,
  OrganizationRole,
} from "@/types/organization"

export function useOrganizationInvitations(
  organizationId: string,
  enabled = true
) {
  return useHTTPQuery<Invitation[]>({
    queryKey: queryKeys.organizations.invitations(organizationId),
    url: API.organizations(organizationId, "invitations"),
    options: { enabled },
  })
}

export function useAcceptInvitation() {
  return useHTTPActionPost<unknown, { token: string }>({
    url: API.invitations("accept"),
  })
}

export function useDeclineInvitation() {
  return useHTTPActionPost<unknown, { token: string }>({
    url: API.invitations("decline"),
  })
}

/** Read-only invitation details, validated the same way accept is, but
 * without committing — backs the "Join {org}?" confirm card. */
export function useInvitationPreview(token: string) {
  return useHTTPQuery<InvitationPreview>({
    queryKey: ["invitations", "preview", token],
    url: `${API.invitations("preview")}?token=${encodeURIComponent(token)}`,
    options: { enabled: !!token, retry: false },
  })
}

/** The caller's own pending invitations across every organization — backs
 * the onboarding auto-surface. */
export function useMyInvitations(enabled = true) {
  return useHTTPQuery<MyInvitation[]>({
    queryKey: queryKeys.account.myInvitations(),
    url: API.me("invitations"),
    options: { enabled, retry: false },
  })
}

/** Notifies the ORIGINAL inviter (not the caller) that a lost/expired
 * invitation needs resending. */
export function useRequestNewInvitation() {
  return useHTTPActionPost<unknown, { token: string }>({
    url: API.invitations("request-new"),
  })
}

export function useRevokeInvitation(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionDelete<void, string>({
    url: (invId) => API.organizations(organizationId, "invitations", invId),
    options: {
      onSuccess: () => {
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.invitations(organizationId),
        })
      },
      onError: () => toast.error(t("organization.invitations.revokeFailed")),
    },
  })
}

export function useInvite(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<
    Invitation,
    { email: string; role: OrganizationRole }
  >({
    url: API.organizations(organizationId, "invitations"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.invitations.sent"))
        capture("member_invited", {
          organization_id: organizationId,
          via: "email",
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.invitations(organizationId),
        })
      },
      onError: (err) =>
        toast.error(
          err?.status?.message ?? t("organization.invitations.sendFailed")
        ),
    },
  })
}

export function useResendInvitation(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<Invitation, { email: string; role: string }>({
    url: API.organizations(organizationId, "invitations"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.invitations.resent"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.invitations(organizationId),
        })
      },
      onError: () => toast.error(t("organization.invitations.resendFailed")),
    },
  })
}

export function useGenerateInviteCode(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<Organization>({
    url: API.organizations(organizationId, "invite-code"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.inviteCode.generated"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.detail(organizationId),
        })
      },
      onError: () => toast.error(t("organization.inviteCode.generateFailed")),
    },
  })
}

export function useToggleInviteCode(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPatch<Organization, { enabled: boolean }>({
    url: API.organizations(organizationId, "invite-code"),
    options: {
      onSuccess: () =>
        queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.detail(organizationId),
        }),
      onError: () => toast.error(t("organization.inviteCode.updateFailed")),
    },
  })
}

/** Read-only invite-code details, validated the same way join is, but
 * without committing — backs the onboarding "Join with an invite code"
 * card's check-then-confirm flow. */
export function useInviteCodePreview(code: string) {
  return useHTTPQuery<InviteCodePreview>({
    queryKey: ["organizations", "join", "preview", code],
    url: `${API.organizations("join", "preview")}?code=${encodeURIComponent(code)}`,
    options: { enabled: code.trim().length === 16, retry: false },
  })
}

export function useJoinOrganization() {
  const queryClient = useQueryClient()
  return useHTTPActionPost<OrganizationView, { code: string }>({
    url: API.organizations("join"),
    options: {
      onSuccess: () =>
        queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.list(),
        }),
    },
  })
}
