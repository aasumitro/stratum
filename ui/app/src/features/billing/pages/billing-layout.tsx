import { Outlet, useParams, Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { Banner } from "@/components/shared/banner"
import { RouteTabs } from "@/components/shared/route-tabs"
import { usePermissions } from "@/hooks/use-permissions"
import { useBillingSubscription, useInvoices } from "@/features/billing/hooks"
import { PendingChangesBar } from "@/features/billing/components/pending-changes-bar"
import { computeDunningBanners } from "@/features/billing/dunning-banners"

// Every billing tab is viewable by any member; only mutations are
// owner-only (the API's own RBAC is what actually enforces this — this
// layout used to block non-owners from billing entirely, which silently
// broke the read-only member/admin view for every tab).
export function BillingLayout() {
  const { t } = useTranslation()
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }
  const { role, isOwner, canViewBilling } = usePermissions()

  const { data: subData } = useBillingSubscription(organizationId)
  const { data: invoicesData } = useInvoices(organizationId, isOwner)
  const sub = subData?.data

  const base = `/organization/${organizationId}/billing`
  const TABS = [
    { label: t("billing.nav.subscription"), suffix: "" },
    ...(isOwner
      ? [
          { label: t("billing.nav.invoices"), suffix: "/invoices" },
          { label: t("billing.nav.history"), suffix: "/history" },
        ]
      : []),
  ]

  if (role !== undefined && !canViewBilling) {
    return null // unreachable in practice — canViewBilling is true for any member
  }

  const dunningBanners = computeDunningBanners(
    sub ?? undefined,
    invoicesData?.data ?? [],
    t
  ).map((b) => ({
    ...b,
    action:
      b.id === "dunning-past-due" || b.id === "dunning-expired" ? (
        <Link
          to={`${base}/invoices` as string}
          className="text-sm font-medium underline"
        >
          {t("billing.dunning.payAction")}
        </Link>
      ) : undefined,
  }))

  return (
    <div className="flex flex-col gap-6">
      {!isOwner && (
        <p className="text-xs text-muted-foreground">
          {t("billing.managedByNote")}
        </p>
      )}
      <Banner banners={dunningBanners} />
      <PendingChangesBar
        organizationId={organizationId}
        nextInvoiceDate={sub?.period_end ?? sub?.trial_end}
        isOwner={isOwner}
      />
      <RouteTabs base={base} tabs={TABS} />
      <Outlet />
    </div>
  )
}
