import { useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { useHTTPQuery } from "@/lib/api/query"
import { useHTTPActionPatch, useHTTPActionUpload } from "@/lib/api/action"
import { queryKeys } from "@/lib/api/keys"
import { API } from "@/lib/api/path"
import type { Organization, OrganizationView } from "@/types/organization"
import type { Country } from "@/types/reference"

export function useCountries() {
  return useHTTPQuery<Country[]>({
    queryKey: queryKeys.references.countries(),
    url: API.references("countries"),
  })
}

export function useOrganizations() {
  return useHTTPQuery<OrganizationView[]>({
    queryKey: queryKeys.organizations.list(),
    url: API.organizations(),
  })
}

export function useOrganization(
  organizationId: string,
  options?: { retry?: boolean }
) {
  return useHTTPQuery<Organization>({
    queryKey: queryKeys.organizations.detail(organizationId),
    url: API.organizations(organizationId),
    options,
  })
}

export function useUpdateOrganization(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPatch<Organization, { name: string; slug: string }>({
    url: API.organizations(organizationId),
    options: {
      onSuccess: () => {
        toast.success(t("organization.settings.updated"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.detail(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.list(),
        })
      },
      onError: () => toast.error(t("organization.settings.updateFailed")),
    },
  })
}

export function useUploadLogo(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionUpload<void>({
    url: API.organizations(organizationId, "logo"),
    fieldName: "logo",
    options: {
      onSuccess: () => {
        toast.success(t("organization.logoUploaded"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.detail(organizationId),
        })
      },
    },
  })
}
