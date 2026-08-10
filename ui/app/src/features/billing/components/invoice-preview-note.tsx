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
  /** Resolved plan display name — falls back to the raw plan id if omitted. */
  planName?: string
  /**
   * True when the subscription already has a pending (unpaid) invoice.
   * `previewInvoice` (backend) always computes `new_period_end` via pure
   * proration regardless of this — but `changePlanWithMetadata` (the real
   * mutation) takes a different path when a pending invoice exists: it
   * voids it and issues a fresh invoice for the new plan, due immediately,
   * not a no-charge period push. Without this flag the proration copy below
   * would promise "no charge today" right before confirming does the
   * opposite.
   */
  hasPendingInvoice?: boolean
}

// Shared by every place that previews a plan/cycle change's commercial
// effect (PlanSelector's own picker, UpgradeWizard, DowngradeWizard,
// ActivateTrialDialog) — all four read the same GET .../billing/preview
// response and must render its two possible shapes identically: proration
// (new_period_end set, no charge today) vs an immediate invoice
// (new_period_end absent, total_cents due).
//
// The line-item breakdown (plan + each addon + discount) is what actually
// explains the total — without it, a total that includes attached addons
// reads as a random number next to the plan's list price.
export function InvoicePreviewNote({
  preview,
  loading,
  currentPeriodEnd,
  className,
  planName,
  hasPendingInvoice,
}: Props) {
  const { t } = useTranslation()
  return (
    <div className={className ?? "rounded-lg bg-muted p-3 text-sm"}>
      {loading || !preview ? (
        <Skeleton className="h-8 w-full" />
      ) : (
        <div className="flex flex-col gap-1.5">
          <div className="flex items-center justify-between">
            <span className="text-muted-foreground">
              {planName ?? preview.plan} · {t(`billing.plans.${preview.cycle}`)}
            </span>
            <span>
              {formatMoney(preview.plan_line_cents, preview.currency)}
            </span>
          </div>
          {(preview.addon_lines ?? []).map((line, i) => (
            <div key={i} className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {line.quantity > 1
                  ? `${line.description} × ${line.quantity}`
                  : line.description}
              </span>
              <span>{formatMoney(line.total_cents, preview.currency)}</span>
            </div>
          ))}
          {!!preview.discount_cents && (
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">
                {t("billing.plans.discountLabel")}
                {preview.coupon_code ? ` (${preview.coupon_code})` : ""}
              </span>
              <span>
                -{formatMoney(preview.discount_cents, preview.currency)}
              </span>
            </div>
          )}
          <div className="flex items-center justify-between border-t pt-1.5 font-medium">
            <span>{t("billing.plans.totalLabel")}</span>
            <span>{formatMoney(preview.total_cents, preview.currency)}</span>
          </div>

          {hasPendingInvoice ? (
            <p className="pt-1 text-muted-foreground">
              <b className="text-foreground">
                {t("billing.plans.voidsPendingInvoiceTitle")}
              </b>{" "}
              {t("billing.plans.voidsPendingInvoiceExplainer")}
            </p>
          ) : preview.new_period_end ? (
            <p className="pt-1 text-muted-foreground">
              <b className="text-foreground">
                {t("billing.plans.noChargeToday")}
              </b>{" "}
              {t("billing.plans.prorationExplainer", {
                newDate: formatDate(preview.new_period_end),
                oldDate: formatDate(currentPeriodEnd),
              })}
            </p>
          ) : (
            <p className="pt-1 text-muted-foreground">
              {t("billing.plans.nextInvoiceTotalNote")}
            </p>
          )}
        </div>
      )}
    </div>
  )
}
