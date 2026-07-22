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

interface Props {
  organizationId: string
  nextInvoiceDate?: string
  isOwner: boolean
}

// "The cart, without a cart": whenever the subscription's live
// plan+addon+coupon composition includes more than the bare plan price,
// show what the next invoice will actually total, with a breakdown.
// Attached addons or an active coupon are the only things that CAN make
// the next invoice differ from a plain plan-only one, so their presence is
// used as the trigger signal rather than diffing against a specific
// historical invoice row (which could differ for unrelated reasons like a
// tax-rate change).
export function PendingChangesBar({
  organizationId,
  nextInvoiceDate,
  isOwner,
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
  const hasAddons = (preview.addon_lines?.length ?? 0) > 0
  const hasCoupon = !!preview.coupon_code
  if (!hasAddons && !hasCoupon) return null

  return (
    <div className="flex flex-col gap-2 rounded-xl border border-blue-500/20 bg-blue-500/10 px-4 py-3 text-sm text-blue-700 dark:text-blue-400">
      <div className="flex flex-wrap items-center gap-2">
        <b>{t("billing.pendingChanges.title")}</b>
        <span className="text-blue-600/70 dark:text-blue-500/70">
          {formatDate(nextInvoiceDate)}
        </span>
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
