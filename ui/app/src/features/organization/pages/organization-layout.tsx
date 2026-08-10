import { useEffect } from "react"
import {
  Link,
  Outlet,
  useNavigate,
  useParams,
  useRouterState,
} from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconCopy } from "@tabler/icons-react"
import { useAuth } from "@/components/auth-provider"
import { useOrganization } from "@/features/organization/hooks/use-organization"
import { useOrganizationMembers } from "@/features/organization/hooks/use-members"
import { useUnsuspendOrganization } from "@/features/organization/hooks/use-settings"
import { usePermissions } from "@/hooks/use-permissions"
import { Button } from "@/components/ui/button"

// Routes accessible even when billing is blocked by a pending invoice.
// Deliberately does NOT include the suspended case — a suspended
// organization's shell should stay fully navigable (read-only), unlike a
// pending-invoice block which restricts to these 3 sections.
const BILLING_ALLOWED_SEGMENTS = new Set([
  "billing",
  "settings",
  "notifications",
])

export function OrganizationLayout() {
  const { t } = useTranslation()
  const { session } = useAuth()
  const navigate = useNavigate()
  const { location } = useRouterState()
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }

  useEffect(() => {
    if (!session) return
    localStorage.setItem("active_organization_id", organizationId)
  }, [organizationId, session])

  const {
    data: wsData,
    isError,
    error,
  } = useOrganization(organizationId, {
    retry: false,
  })

  useEffect(() => {
    // Signing out clears the session cookie and then queryClient.clear()s
    // (see auth-provider.tsx), which forces this still-mounted query to
    // refetch immediately — with no token, a beat before the /login
    // navigation actually unmounts this page. That 401 is an artifact of
    // logging out on purpose, not a real "access denied", so it's only
    // worth surfacing while a session still exists.
    if (!isError || !session) return
    const msg =
      (error as { error?: string })?.error ?? t("organization.accessDenied")
    localStorage.removeItem("active_organization_id")
    toast.error(msg)
    void navigate({ to: "/organizations" })
  }, [isError, error, navigate, t, session])

  const { isOwner, hasPendingInvoice, role, canViewBilling } = usePermissions()
  const organization = wsData?.data
  const isSuspended = organization?.status === "suspended"

  // Billing-caused suspension is the one that resolves itself by
  // payment (the "subscription expired" sentinel billing's auto-suspend
  // uses) — a self-suspended organization has no invoice to point to.
  const isBillingCausedSuspension =
    isSuspended && organization?.suspended_reason === "subscription expired"

  const { data: membersData } = useOrganizationMembers(organizationId, {
    enabled: isSuspended && !isOwner,
  })
  const owner = (membersData?.data ?? []).find(
    (m) => m.auth_sub === organization?.owner_id
  )
  const { mutate: unsuspend, isPending: unsuspending } =
    useUnsuspendOrganization(organizationId)

  // Redirect billing-blocked users away from restricted routes. Suspended
  // organizations are deliberately NOT redirected — every page stays
  // reachable, read-only.
  const subSegments = location.pathname.split("/").filter(Boolean).slice(2) // drop "organization" and organizationId
  const currentSegment = subSegments[0]
  useEffect(() => {
    if (!hasPendingInvoice || !currentSegment) return
    if (BILLING_ALLOWED_SEGMENTS.has(currentSegment)) return
    void navigate({
      to: "/organization/$organizationId/billing",
      params: { organizationId },
    })
  }, [hasPendingInvoice, currentSegment, navigate, organizationId])

  // Top-level segments below a role's threshold (PERMISSION_MATRIX) hide
  // from the sidebar (sidebar-organization-nav.tsx's ORG_NAV `.filter`),
  // but the route itself is still reachable by typing the URL directly —
  // send those visitors to Settings instead, same as a role-gated Settings
  // section simply not being in the DOM. Segment→allowed mirrors ORG_NAV's
  // own `allowed` fields, so a future nav item only needs an entry here.
  const isSegmentAllowed = currentSegment === "billing" ? canViewBilling : true
  useEffect(() => {
    if (role === undefined || !currentSegment || isSegmentAllowed) return
    void navigate({
      to: "/organization/$organizationId/settings",
      params: { organizationId },
    })
  }, [role, isSegmentAllowed, currentSegment, navigate, organizationId])

  return (
    <div className="flex flex-1 flex-col gap-6">
      {/* Suspended lockout: shell stays, banner names who can help
          (member/admin) or the reason + fix (owner). Every page below
          stays reachable; UI-only mutation gating happens per-page. */}
      {isSuspended && !isOwner && (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-amber-500/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-700 dark:text-amber-400">
          <span>
            {owner
              ? t("organization.suspendedMemberBanner", {
                  ownerName:
                    owner.full_name ||
                    owner.email ||
                    t("organization.roles.owner"),
                })
              : t("organization.suspendedBanner")}
          </span>
          {owner?.email && (
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                void navigator.clipboard.writeText(owner.email!)
                toast.success(t("organization.copyOwnerEmailSuccess"))
              }}
            >
              <IconCopy data-icon="inline-start" />
              {t("organization.copyOwnerEmail")}
            </Button>
          )}
        </div>
      )}
      {isSuspended && isOwner && (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-amber-500/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-700 dark:text-amber-400">
          <span>
            {isBillingCausedSuspension
              ? t("organization.suspendedOwnerBillingBanner")
              : t("organization.suspendedOwnerSelfBanner", {
                  reason: organization?.suspended_reason,
                })}
          </span>
          {isBillingCausedSuspension ? (
            <Link
              to="/organization/$organizationId/billing"
              params={{ organizationId }}
              className="text-sm font-medium underline"
            >
              {t("organization.pendingInvoicePay")}
            </Link>
          ) : (
            <Button
              size="sm"
              variant="outline"
              disabled={unsuspending}
              onClick={() => unsuspend()}
            >
              {t("organization.danger.unsuspendAction")}
            </Button>
          )}
        </div>
      )}

      {hasPendingInvoice && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          {t("organization.pendingInvoiceBanner")}{" "}
          <Link
            to="/organization/$organizationId/billing"
            params={{ organizationId }}
            className="font-medium underline"
          >
            {t("organization.pendingInvoicePay")}
          </Link>
        </div>
      )}

      {/* Keyed by organizationId: switching organizations — including
          editing the URL directly — only changes this param, it doesn't
          remount the route. Without a key, any local state in a page below
          here (an open dialog, a pending mutation, a selected id) survives
          the switch and can end up acting against the new organization. */}
      <Outlet key={organizationId} />
    </div>
  )
}
