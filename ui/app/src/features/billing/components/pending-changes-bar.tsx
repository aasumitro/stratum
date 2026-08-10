import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconChevronDown, IconChevronUp } from "@tabler/icons-react"
import { useInvoicePreview } from "@/features/billing/hooks"
import { formatMoney } from "@/lib/format"

function formatDate(s?: string) {
  if (!s) return "—"
  return new Date(s).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })
}

// Date.now() must not be called inline in the component body (impure
// during render, see subscription-card.tsx's identically-shaped
// trialDaysLeft) — a plain top-level function keeps it out of render.
function daysUntil(iso?: string): number {
  if (!iso) return Infinity
  return (new Date(iso).getTime() - Date.now()) / 86_400_000
}

// Renewal within this many days counts as "coming up soon" for this bar's
// second trigger condition, below.
const NEAR_RENEWAL_DAYS = 30

interface Props {
  organizationId: string
  nextInvoiceDate?: string
  isOwner: boolean
  hasPendingInvoice: boolean
}

// A heads-up on what's about to be charged, shown only when it's actually
// relevant: there's already an unpaid invoice, or renewal is coming up soon
// (NEAR_RENEWAL_DAYS) — not merely because add-ons/a coupon are attached,
// which could be true for the entire lifetime of the subscription. The
// breakdown content itself (plan + addon lines + coupon) still comes from
// the live preview regardless of which condition triggered it.
export function PendingChangesBar({
  organizationId,
  nextInvoiceDate,
  isOwner,
  hasPendingInvoice,
}: Props) {
  const { t } = useTranslation()
  const [expanded, setExpanded] = useState(false)
  const { data } = useInvoicePreview(
    organizationId,
    undefined,
    undefined,
    isOwner
  )
  const preview = data?.data

  if (!isOwner || !preview) return null
  const isNearRenewal = daysUntil(nextInvoiceDate) <= NEAR_RENEWAL_DAYS
  if (!hasPendingInvoice && !isNearRenewal) return null
  const hasCoupon = !!preview.coupon_code

  // The date only means "at your next renewal" when that's actually why
  // this is showing — a pending invoice can exist long before renewal
  // (e.g. a new org's first unpaid invoice), so pairing it with a
  // far-future renewal date would misattribute it, the same wrong-date
  // confusion already fixed once on the plan-change wizards.
  return (
    <div className="flex flex-col gap-2 rounded-xl border border-blue-500/20 bg-blue-500/10 px-4 py-3 text-sm text-blue-700 dark:text-blue-400">
      <div className="flex flex-wrap items-center gap-2">
        {isNearRenewal ? (
          <>
            <b>{t("billing.pendingChanges.title")}</b>
            <span className="text-blue-600/70 dark:text-blue-500/70">
              {formatDate(nextInvoiceDate)}
            </span>
          </>
        ) : (
          <b>{t("billing.pendingChanges.titlePendingInvoice")}</b>
        )}
        <span className="flex-1" />
        <b>{formatMoney(preview.total_cents, preview.currency)}</b>
        <button
          type="button"
          className="flex items-center gap-1 underline underline-offset-2"
          onClick={() => setExpanded((v) => !v)}
        >
          {t("billing.pendingChanges.breakdown")}
          {expanded ? (
            <IconChevronUp className="size-3.5" />
          ) : (
            <IconChevronDown className="size-3.5" />
          )}
        </button>
      </div>
      {expanded && (
        <div className="flex flex-col gap-1 rounded-lg bg-background/60 p-3 text-foreground">
          <div className="flex items-center justify-between text-xs">
            <span className="capitalize">
              {preview.plan} · {preview.cycle}
            </span>
            <span>
              {formatMoney(preview.plan_line_cents, preview.currency)}
            </span>
          </div>
          {preview.addon_lines?.map((line, i) => (
            <div key={i} className="flex items-center justify-between text-xs">
              <span>
                {line.description}
                {line.quantity > 1 ? ` × ${line.quantity}` : ""}
              </span>
              <span>{formatMoney(line.total_cents, preview.currency)}</span>
            </div>
          ))}
          {hasCoupon && (
            <div className="flex items-center justify-between text-xs text-emerald-600">
              <span>{preview.coupon_code}</span>
              <span>
                −{formatMoney(preview.discount_cents ?? 0, preview.currency)}
              </span>
            </div>
          )}
          <div className="mt-1 flex items-center justify-between border-t pt-1 text-xs font-semibold">
            <span>{t("billing.pendingChanges.total")}</span>
            <span>{formatMoney(preview.total_cents, preview.currency)}</span>
          </div>
        </div>
      )}
    </div>
  )
}
