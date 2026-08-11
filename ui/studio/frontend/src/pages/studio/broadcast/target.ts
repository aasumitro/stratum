import type {
  OrganizationSummary,
  UserResult,
} from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"

export type TargetType = "all" | "organization" | "user"

export function buildTarget(
  type: TargetType,
  organization: OrganizationSummary | null,
  user: UserResult | null
): string {
  if (type === "all") return "all"
  if (type === "organization")
    return organization ? `organization:${organization.id}` : ""
  return user ? `user:${user.auth_sub}` : ""
}

export function formatTarget(
  target: string,
  organization: OrganizationSummary | null,
  user: UserResult | null
): string {
  if (target === "all") return "All users"
  if (target.startsWith("organization:"))
    return organization
      ? `${organization.name} (${organization.slug})`
      : `Organization: ${target.slice(13).slice(0, 8)}…`
  if (target.startsWith("user:"))
    return user
      ? `${user.full_name} — ${user.email}`
      : `User: ${target.slice(5).slice(0, 12)}…`
  return target
}
