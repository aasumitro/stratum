import type { QueryClient } from "@tanstack/react-query"
import { getFn } from "@/lib/api/query"
import { queryKeys } from "@/lib/api/keys"
import { API } from "@/lib/api/path"
import type { HTTPResponse } from "@/lib/api/response"
import type { UserProfile } from "@/types/account"
import type { OrganizationView } from "@/types/organization"

export type LandingRoute =
  | { to: "/organization/$organizationId"; params: { organizationId: string } }
  | { to: "/organizations" }

/**
 * Landing resolution order: server-stored default org -> last-opened
 * org (localStorage) -> only org -> organization list. A stored id that no
 * longer matches a membership (e.g. the user left that organization) is
 * simply skipped — no separate "clear on leave" write path is needed since
 * this always re-validates against the live membership list.
 */
export async function resolveLandingRoute(
  queryClient: QueryClient
): Promise<LandingRoute> {
  const [profileRes, orgsRes] = await Promise.all([
    queryClient.fetchQuery<HTTPResponse<UserProfile>>({
      queryKey: queryKeys.account.me(),
      queryFn: getFn<UserProfile>(API.me()),
      staleTime: 30_000,
    }),
    queryClient.fetchQuery<HTTPResponse<OrganizationView[]>>({
      queryKey: queryKeys.organizations.list(),
      queryFn: getFn<OrganizationView[]>(API.organizations()),
      staleTime: 30_000,
    }),
  ])

  const organizations = orgsRes.data ?? []
  const isMember = (id: string) => organizations.some((o) => o.id === id)

  const defaultId = profileRes.data?.preferences?.default_organization_id as
    | string
    | undefined
  if (defaultId && isMember(defaultId)) {
    return {
      to: "/organization/$organizationId",
      params: { organizationId: defaultId },
    }
  }

  const lastOpenedId = localStorage.getItem("active_organization_id")
  if (lastOpenedId && isMember(lastOpenedId)) {
    return {
      to: "/organization/$organizationId",
      params: { organizationId: lastOpenedId },
    }
  }

  if (organizations.length === 1) {
    return {
      to: "/organization/$organizationId",
      params: { organizationId: organizations[0].id },
    }
  }

  return { to: "/organizations" }
}
