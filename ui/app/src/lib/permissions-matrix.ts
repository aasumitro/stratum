import type {
  OrganizationRole,
  PermissionFeature,
  PermissionAction,
} from "@/types/organization"

const ROLE_RANK: Record<OrganizationRole, number> = {
  member: 0,
  admin: 1,
  owner: 2,
}

export const PERMISSION_MATRIX: Record<
  PermissionFeature,
  Partial<Record<PermissionAction, OrganizationRole>>
> = {
  members: { view: "member", manage: "admin" },
  invitations: { view: "admin", manage: "admin" },
  files: { view: "admin" },
  webhooks: { view: "owner" },
  auditLog: { view: "admin" },
  billing: { view: "admin", manage: "owner" },
  settingsGeneral: { view: "member", edit: "owner" },
  settingsSecurity: { view: "admin", edit: "owner" },
  settingsDanger: { view: "owner", delete: "owner" },
}

export function hasPermission(
  role: OrganizationRole | undefined,
  feature: PermissionFeature,
  action: PermissionAction
): boolean {
  const required = PERMISSION_MATRIX[feature]?.[action]
  return !!required && !!role && ROLE_RANK[role] >= ROLE_RANK[required]
}
