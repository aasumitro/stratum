import { Outlet, useParams } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { IconLoader2 } from "@tabler/icons-react"
import { Banner } from "@/components/shared/banner"
import { Button } from "@/components/ui/button"
import { usePermissions } from "@/hooks/use-permissions"
import {
  useBillingSubscription,
  useInvoices,
  useResumeSubscription,
} from "@/features/billing/hooks"
import { PendingChangesBar } from "@/features/billing/components/pending-changes-bar"
import { computeDunningBanners } from "@/features/billing/dunning-banners"

// Billing is a single page (Plan, Usage, Invoices, add-ons) — viewable by
// admin+ only (a plain member shouldn't know billing exists at all, see
// PERMISSION_MATRIX); mutations are further narrowed to owner-only (the
// API's own RBAC is what actually enforces this). A member who hits this
// route directly by URL is redirected by organization-layout.tsx's effect
// before this ever mounts with real data — this guard only covers the one
// render in between.
export function BillingLayout() {
  const { t } = useTranslation()
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }
  const { role, isOwner, canViewBilling, hasPendingInvoice } = usePermissions()

  const { data: subData } = useBillingSubscription(organizationId)
  const { data: invoicesData } = useInvoices(organizationId, isOwner)
  const sub = subData?.data
  const { mutate: resume, isPending: resuming } =
    useResumeSubscription(organizationId)

  if (role !== undefined && !canViewBilling) {
    return null
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
      ) : b.id === "dunning-cancelled" && isOwner ? (
        <Button
          size="sm"
          variant="outline"
          disabled={resuming}
          onClick={() => resume()}
        >
          {resuming && (
            <IconLoader2 data-icon="inline-start" className="animate-spin" />
          )}
          {t("billing.subscription.resume")}
        </Button>
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
        hasPendingInvoice={hasPendingInvoice}
      />
      <Outlet />
    </div>
  )
}
