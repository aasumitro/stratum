import { useParams } from "@tanstack/react-router"
import { useOrganizations } from "@/features/organization/hooks"
import type { OrganizationView } from "@/types/organization"

interface ActiveOrganization {
  organizationId: string | undefined
  organization: OrganizationView | undefined
  role: OrganizationView["role"] | undefined
  isLoading: boolean
}

/**
 * Single source for "what organization is the current route in, and what is
 * the caller's role in it" — replaces the pathname-regex + localStorage
 * re-derivation that used to be duplicated across AppHeader,
 * SidebarOrganizationNav, OrganizationBanner, and OrganizationLayout.
 */
export function useActiveOrganization(): ActiveOrganization {
  const { organizationId } = useParams({ strict: false }) as {
    organizationId?: string
  }
  const { data, isLoading } = useOrganizations()
  const organizations = data?.data ?? []
  const organization = organizationId
    ? organizations.find((o) => o.id === organizationId)
    : undefined

  return {
    organizationId,
    organization,
    role: organization?.role,
    isLoading,
  }
}
