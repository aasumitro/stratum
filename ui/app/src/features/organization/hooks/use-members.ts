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
import { handleHttpError } from "@/lib/api/error"
import { useUpdatePreferences } from "@/features/account/hooks"
import type { Member, OrganizationRole } from "@/types/organization"

export function useOrganizationMembers(
  organizationId: string,
  options?: { enabled?: boolean }
) {
  return useHTTPQuery<Member[]>({
    queryKey: queryKeys.organizations.members(organizationId),
    url: API.organizations(organizationId, "members"),
    options,
  })
}

export function useChangeMemberRole(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPatch<
    Member,
    { authSub: string; role: OrganizationRole }
  >({
    url: ({ authSub }) =>
      API.organizations(organizationId, "members", authSub, "role"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.members.roleUpdated"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.members(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.list(),
        })
      },
      onError: () => toast.error(t("organization.members.roleUpdateFailed")),
    },
  })
}

export function useRemoveMember(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionDelete<void, string>({
    url: (authSub) => API.organizations(organizationId, "members", authSub),
    options: {
      onSuccess: () => {
        toast.success(t("organization.members.removed"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.members(organizationId),
        })
      },
      onError: () => toast.error(t("organization.members.removeFailed")),
    },
  })
}

export function useSuspendMember(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<void, string>({
    url: (authSub) =>
      API.organizations(organizationId, "members", authSub, "suspend"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.members.suspended"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.members(organizationId),
        })
      },
      onError: () => toast.error(t("organization.members.suspendFailed")),
    },
  })
}

export function useReinstateMember(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<void, string>({
    url: (authSub) =>
      API.organizations(organizationId, "members", authSub, "reinstate"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.members.reinstated"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.members(organizationId),
        })
      },
      onError: () => toast.error(t("organization.members.reinstateFailed")),
    },
  })
}

export function useLeaveOrganization(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  const { mutate: updatePreferences } = useUpdatePreferences()
  return useHTTPActionDelete<void>({
    url: API.organizations(organizationId, "leave"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.members.leftOrganization"))
        if (localStorage.getItem("active_organization_id") === organizationId) {
          localStorage.removeItem("active_organization_id")
          updatePreferences(
            { default_organization_id: null },
            { onError: handleHttpError }
          )
        }
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.list(),
        })
      },
      onError: () => toast.error(t("organization.members.leaveFailed")),
    },
  })
}

export function useTransferOwnership(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<void, { auth_sub: string }>({
    url: API.organizations(organizationId, "transfer"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.members.transferred"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.members(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.list(),
        })
      },
      onError: () => toast.error(t("organization.members.transferFailed")),
    },
  })
}

export interface ImportRow {
  email: string
  role: OrganizationRole
}
export interface ImportRowResult {
  index: number
  email: string
  reason: string
}

/** Bulk-invites by email with a dry-run preview — pass `dry_run:
 * true` to validate without committing, `false` to actually create the
 * invitations. Both modes return the same per-row error shape. */
export function useImportMembers(organizationId: string) {
  const queryClient = useQueryClient()
  return useHTTPActionPost<
    {
      valid_count?: number
      imported?: number
      errors?: ImportRowResult[]
      failed?: ImportRowResult[]
    },
    { rows: ImportRow[]; dry_run: boolean }
  >({
    url: API.organizations(organizationId, "members", "import"),
    options: {
      onSuccess: () => {
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.invitations(organizationId),
        })
      },
    },
  })
}
