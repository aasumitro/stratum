import { useTranslation } from "react-i18next"
import { Skeleton } from "@/components/ui/skeleton"
import { formatMoney } from "@/lib/format"
import type { InvoicePreview } from "@/types/billing"

function formatDate(s?: string) {
  if (!s) return "—"
  return new Date(s).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })
}

interface Props {
  preview?: InvoicePreview | null
  loading: boolean
  currentPeriodEnd?: string
  className?: string
}

// Shared by every place that previews a plan/cycle change's commercial
// effect (PlanSelector's own picker, UpgradeWizard, DowngradeWizard) — all
// three read the same GET .../billing/preview response and must render its
// two possible shapes identically: proration (new_period_end set, no charge
// today) vs an immediate invoice (new_period_end absent, total_cents due).
export function InvoicePreviewNote({
  preview,
  loading,
  currentPeriodEnd,
  className,
}: Props) {
  const { t } = useTranslation()
  return (
    <div className={className ?? "rounded-lg bg-muted p-3 text-sm"}>
      {loading || !preview ? (
        <Skeleton className="h-8 w-full" />
      ) : preview.new_period_end ? (
        <p>
          <b>{t("billing.plans.noChargeToday")}</b>{" "}
          {t("billing.plans.prorationExplainer", {
            newDate: formatDate(preview.new_period_end),
            oldDate: formatDate(currentPeriodEnd),
          })}
        </p>
      ) : (
        <p>
          {t("billing.plans.nextInvoiceTotal", {
            amount: formatMoney(preview.total_cents, preview.currency),
          })}
        </p>
      )}
    </div>
  )
}
