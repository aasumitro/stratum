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
import {
  isBillingSuspensionReason,
  isSelfSuspensionReason,
} from "@/hooks/use-billing-status"
import { Banner, type BannerItem } from "@/components/shared/banner"
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

  const {
    isOwner,
    hasPendingInvoice,
    hasBlockingInvoice,
    role,
    canViewBilling,
  } = usePermissions()
  const organization = wsData?.data
  const isSuspended = organization?.status === "suspended"

  // Billing-caused suspension is the one that resolves itself by payment
  // (the reasons billing writes when a subscription expires or its
  // cancellation takes effect) — a self-suspended organization has no
  // invoice to point to.
  const isBillingCausedSuspension =
    isSuspended && isBillingSuspensionReason(organization?.suspended_reason)
  // Only the owner's own pause can be lifted from here; any other reason was
  // set by support, which lifts it in Studio.
  const isSelfSuspension =
    isSuspended && isSelfSuspensionReason(organization?.suspended_reason)

  const { data: membersData } = useOrganizationMembers(organizationId, {
    enabled: isSuspended && !isOwner,
  })
  const owner = (membersData?.data ?? []).find(
    (m) => m.auth_sub === organization?.owner_id
  )
  const { mutate: unsuspend, isPending: unsuspending } =
    useUnsuspendOrganization(organizationId)

  // Redirect owners with an unpaid activation away from restricted routes.
  // Suspended organizations are deliberately NOT redirected — every page
  // stays reachable, read-only.
  const subSegments = location.pathname.split("/").filter(Boolean).slice(2) // drop "organization" and organizationId
  const currentSegment = subSegments[0]
  useEffect(() => {
    if (!hasBlockingInvoice || !currentSegment) return
    if (BILLING_ALLOWED_SEGMENTS.has(currentSegment)) return
    void navigate({
      to: "/organization/$organizationId/billing",
      params: { organizationId },
    })
  }, [hasBlockingInvoice, currentSegment, navigate, organizationId])

  // One billing banner at a time. On the billing page itself,
  // billing-layout.tsx renders the subscription's state banner with the
  // action that resolves it, so a billing-caused suspension or pending-invoice
  // banner here would only repeat that state and link to the page it's on —
  // they step aside there. A non-billing suspension still shows: billing has
  // nothing to say about it. Elsewhere a suspended owner's banner already
  // covers the unpaid invoice, so the pending-invoice banner waits its turn.
  const onBillingPage = currentSegment === "billing"
  const showSuspendedBanner =
    isSuspended && !(onBillingPage && isBillingCausedSuspension)
  const showPendingInvoiceBanner =
    hasBlockingInvoice && !isSuspended && !onBillingPage
  // An expired subscription only has an invoice to pay once the owner
  // resumes it — until then, "pay the unpaid invoice" would point at nothing.
  const ownerBillingBannerKey = hasPendingInvoice
    ? "organization.suspendedOwnerBillingBanner"
    : "organization.suspendedOwnerExpiredBanner"

  const banners: BannerItem[] = []
  if (showSuspendedBanner && !isOwner) {
    banners.push({
      id: "suspended",
      severity: "warning",
      message: owner
        ? t("organization.suspendedMemberBanner", {
            ownerName:
              owner.full_name || owner.email || t("organization.roles.owner"),
          })
        : t("organization.suspendedBanner"),
      action: owner?.email ? (
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
      ) : undefined,
    })
  }
  if (showSuspendedBanner && isOwner) {
    banners.push({
      id: "suspended",
      severity: "warning",
      message: isBillingCausedSuspension
        ? t(ownerBillingBannerKey)
        : isSelfSuspension
          ? t("organization.suspendedOwnerSelfBanner")
          : t("organization.suspendedOwnerSupportBanner", {
              reason: organization?.suspended_reason,
            }),
      action: isBillingCausedSuspension ? (
        <Link
          to="/organization/$organizationId/billing"
          params={{ organizationId }}
          className="text-sm font-medium underline"
        >
          {t("organization.pendingInvoicePay")}
        </Link>
      ) : isSelfSuspension ? (
        <Button
          size="sm"
          variant="outline"
          disabled={unsuspending}
          onClick={() => unsuspend()}
        >
          {t("organization.danger.unsuspendAction")}
        </Button>
      ) : undefined,
    })
  }
  if (showPendingInvoiceBanner) {
    banners.push({
      id: "pending-invoice",
      severity: "critical",
      message: t("organization.pendingInvoiceBanner"),
      action: (
        <Link
          to="/organization/$organizationId/billing"
          params={{ organizationId }}
          className="text-sm font-medium underline"
        >
          {t("organization.pendingInvoicePay")}
        </Link>
      ),
    })
  }

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
      <Banner banners={banners} />

      {/* Keyed by organizationId: switching organizations — including
          editing the URL directly — only changes this param, it doesn't
          remount the route. Without a key, any local state in a page below
          here (an open dialog, a pending mutation, a selected id) survives
          the switch and can end up acting against the new organization. */}
      <Outlet key={organizationId} />
    </div>
  )
}
