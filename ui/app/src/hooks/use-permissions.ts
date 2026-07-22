import { useActiveOrganization } from "@/hooks/use-active-organization"
import { useBillingStatus } from "@/hooks/use-billing-status"
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
  canEditSettings: boolean
  canUploadLogo: boolean
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
    canManageMembers: isAdminUp,
    canViewBilling: !!role,
    canActOnBilling: isOwner,
    canAccessFiles: isAdminUp,
    canAccessWebhooks: isOwner,
    canAccessAuditLog: isAdminUp,
    canEditSettings: isOwner,
    canUploadLogo: isAdminUp,
  }
}
