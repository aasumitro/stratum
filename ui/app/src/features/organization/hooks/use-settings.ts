import { useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import {
  useHTTPActionPatch,
  useHTTPActionPost,
  useHTTPActionDelete,
} from "@/lib/api/action"
import { queryKeys } from "@/lib/api/keys"
import { API } from "@/lib/api/path"
import { handleHttpError } from "@/lib/api/error"
import { useUpdatePreferences } from "@/features/account/hooks"
import type { Organization } from "@/types/organization"

export function useUpdateOrganizationSettings(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPatch<
    Organization,
    { timezone: string; locale: string; allowed_ips?: string[] }
  >({
    url: API.organizations(organizationId, "settings"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.settings.settingsUpdated"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.detail(organizationId),
        })
      },
      onError: () =>
        toast.error(t("organization.settings.settingsUpdateFailed")),
    },
  })
}

export function useDeleteOrganization(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  const { mutate: updatePreferences } = useUpdatePreferences()
  return useHTTPActionDelete<void>({
    url: API.organizations(organizationId),
    options: {
      onSuccess: () => {
        toast.success(t("organization.danger.deleted"))
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
      onError: () => toast.error(t("organization.danger.deleteFailed")),
    },
  })
}

// Danger zone — self-service pause/resume, distinct from the
// billing-driven or Studio-operator-driven suspend paths.
export function useSuspendOrganization(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<void, { reason?: string }>({
    url: API.organizations(organizationId, "suspend"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.danger.suspended"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.detail(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.list(),
        })
      },
      onError: () => toast.error(t("organization.danger.suspendFailed")),
    },
  })
}

export function useUnsuspendOrganization(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<void, void>({
    url: API.organizations(organizationId, "unsuspend"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.danger.unsuspended"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.detail(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.list(),
        })
      },
      onError: (err) => {
        const code = (err as { status?: { code?: string } })?.status?.code
        toast.error(
          code === "BILLING_HOLD"
            ? t("organization.danger.unsuspendBillingHold")
            : t("organization.danger.unsuspendFailed")
        )
      },
    },
  })
}
