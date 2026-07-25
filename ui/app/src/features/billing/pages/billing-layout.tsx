import { Outlet, useParams } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { Banner } from "@/components/shared/banner"
import { usePermissions } from "@/hooks/use-permissions"
import { useBillingSubscription, useInvoices } from "@/features/billing/hooks"
import { PendingChangesBar } from "@/features/billing/components/pending-changes-bar"
import { computeDunningBanners } from "@/features/billing/dunning-banners"

// Billing is a single page (Plan, Usage, Invoices, add-ons) — every part
// of it is viewable by any member; only mutations are owner-only (the
// API's own RBAC is what actually enforces this).
export function BillingLayout() {
  const { t } = useTranslation()
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }
  const { role, isOwner, canViewBilling } = usePermissions()

  const { data: subData } = useBillingSubscription(organizationId)
  const { data: invoicesData } = useInvoices(organizationId, isOwner)
  const sub = subData?.data

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
        // Invoices live inline on this same page — a plain in-page
        // anchor to the Invoices card instead of a route Link, which
        // would just navigate to the page it's already rendered on.
        <a href="#billing-invoices" className="text-sm font-medium underline">
          {t("billing.dunning.payAction")}
        </a>
      ) : undefined,
  }))

  return (
    <div className="flex flex-col gap-6">
      {/* Non-owner note is shown once, in context, where the mutation
          button it explains the absence of would be — see
          SubscriptionCard's header — not repeated page-wide here. */}
      <Banner banners={dunningBanners} />
      <PendingChangesBar
        organizationId={organizationId}
        nextInvoiceDate={sub?.period_end ?? sub?.trial_end}
        isOwner={isOwner}
      />
      <Outlet />
    </div>
  )
}
