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
import { IconBuilding, IconCopy } from "@tabler/icons-react"
import { useAuth } from "@/components/auth-provider"
import {
  useOrganization,
  useOrganizationMembers,
  useUnsuspendOrganization,
} from "@/features/organization/hooks"
import { usePermissions } from "@/hooks/use-permissions"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Separator } from "@/components/ui/separator"
import { cn } from "@/lib/ui"

// Routes accessible even when billing is blocked by a pending invoice.
// Deliberately does NOT include the suspended case — a suspended
// organization's shell should stay fully navigable (read-only), unlike a
// pending-invoice block which restricts to these 3 sections.
const BILLING_ALLOWED_SEGMENTS = new Set([
  "billing",
  "settings",
  "notifications",
])

const STATUS_BADGE: Record<string, string> = {
  active: "bg-emerald-500/10 text-emerald-600",
  suspended: "bg-amber-500/10 text-amber-600",
  deleted: "bg-destructive/10 text-destructive",
}

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
    isLoading: wsLoading,
    isError,
    error,
  } = useOrganization(organizationId, { retry: false })

  useEffect(() => {
    if (!isError) return
    const msg =
      (error as { error?: string })?.error ?? t("organization.accessDenied")
    localStorage.removeItem("active_organization_id")
    toast.error(msg)
    void navigate({ to: "/organizations" })
  }, [isError, error, navigate, t])

  const { role, isOwner, hasPendingInvoice } = usePermissions()
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

  return (
    <div className="flex flex-1 flex-col gap-6">
      <div className="flex items-center gap-3">
        <Link
          to="/organization/$organizationId/members"
          params={{ organizationId }}
          className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-muted transition-colors hover:bg-muted/80"
        >
          <IconBuilding className="size-5 text-muted-foreground" />
        </Link>
        {wsLoading ? (
          <div className="flex flex-col gap-1.5">
            <Skeleton className="h-5 w-32" />
            <Skeleton className="h-3.5 w-20" />
          </div>
        ) : (
          <div className="flex flex-col gap-0.5">
            <div className="flex items-center gap-2">
              <span className="text-base font-semibold">
                {organization?.name}
              </span>
              {organization?.status && (
                <span
                  className={cn(
                    "rounded-full px-2 py-0.5 text-xs font-medium capitalize",
                    STATUS_BADGE[organization.status] ?? ""
                  )}
                >
                  {organization.status}
                </span>
              )}
              {role && (
                <Badge variant="outline" className="text-xs capitalize">
                  {role}
                </Badge>
              )}
            </div>
            <span className="text-sm text-muted-foreground">
              {organization?.slug}
            </span>
          </div>
        )}
      </div>

      <Separator />

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

      <Outlet />
    </div>
  )
}
