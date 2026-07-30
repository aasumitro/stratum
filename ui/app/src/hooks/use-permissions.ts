import { useActiveOrganization } from "@/hooks/use-active-organization"
import { useBillingStatus } from "@/hooks/use-billing-status"
import { hasPermission } from "@/lib/permissions-matrix"
import type { OrganizationRole } from "@/types/organization"

export interface Permissions {
  role: OrganizationRole | undefined
  isOwner: boolean
  isAdminUp: boolean
  isBillingBlocked: boolean
  isSuspended: boolean
  // Sidebar visibility matrix — one source of truth so the
  // matrix can't drift between the nav, the page guard, and the API's own
  // RBAC (which remains the actual enforcement).
  canManageMembers: boolean
  canViewBilling: boolean
  canActOnBilling: boolean
  canAccessFiles: boolean
  canAccessWebhooks: boolean
  canAccessAuditLog: boolean
  canAccessSecurity: boolean
  canEditSettings: boolean
  canUploadLogo: boolean
  hasPendingInvoice: boolean
}

export function usePermissions(): Permissions {
  const { role } = useActiveOrganization()
  const billing = useBillingStatus()
  const isOwner = role === "owner"
  const isAdminUp = isOwner || role === "admin"

  return {
    role,
    isOwner,
    isAdminUp,
    isBillingBlocked: billing.isBillingBlocked,
    isSuspended: billing.isSuspended,
    canManageMembers: hasPermission(role, "members", "manage"),
    canViewBilling: hasPermission(role, "billing", "view"),
    canActOnBilling: hasPermission(role, "billing", "manage"),
    canAccessFiles: hasPermission(role, "files", "view"),
    canAccessWebhooks: hasPermission(role, "webhooks", "view"),
    canAccessAuditLog: hasPermission(role, "auditLog", "view"),
    canAccessSecurity: hasPermission(role, "settingsSecurity", "view"),
    canEditSettings: hasPermission(role, "settingsGeneral", "edit"),
    // admin+ (no matching matrix action — settingsGeneral.edit is owner-only, which is narrower than logo upload's real permission)
    canUploadLogo: isAdminUp,
    hasPendingInvoice: billing.hasPendingInvoice,
  }
}
