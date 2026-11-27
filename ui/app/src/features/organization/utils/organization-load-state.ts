import { isRefusal } from "@/lib/api/error"

export type OrganizationLoadState = "ok" | "mfa" | "refused" | "unavailable"

// What the organization layout does with the organization query's result:
// "mfa" shows the MFA gate (the member keeps the organization), "refused"
// (401/403/404) forgets the organization and leaves, "unavailable" offers a
// retry when nothing is loaded yet. A failed refetch with data keeps the page.
export function organizationLoadState(
  isError: boolean,
  error: unknown,
  hasData: boolean
): OrganizationLoadState {
  if (!isError) return "ok"
  const code = (error as { status?: { code?: string } } | null)?.status?.code
  if (code === "ORGANIZATION_MFA_REQUIRED") return "mfa"
  if (isRefusal(error)) return "refused"
  return hasData ? "ok" : "unavailable"
}
