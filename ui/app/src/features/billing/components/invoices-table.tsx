import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconChevronDown } from "@tabler/icons-react"
import {
  Card,
  CardAction,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { PdfButton } from "@/features/billing/components/pdf-button"
import { PreviewButton } from "@/features/billing/components/preview-button"
import { PayButton } from "@/features/billing/components/pay-button"
import { RegenerateButton } from "@/features/billing/components/regenerate-button"
import {
  useInvoices,
  usePaymentLinks,
  usePayments,
} from "@/features/billing/hooks"
import { usePermissions } from "@/hooks/use-permissions"
import { formatMoney } from "@/lib/format"
import { cn } from "@/lib/ui"

const STATUS_BADGE: Record<string, string> = {
  paid: "bg-emerald-500/10 text-emerald-600",
  pending: "bg-amber-500/10 text-amber-600",
  failed: "bg-destructive/10 text-destructive",
  void: "bg-muted text-muted-foreground",
}

const PAYMENT_STATUS_BADGE: Record<string, string> = {
  paid: "bg-emerald-500/10 text-emerald-600",
  pending: "bg-amber-500/10 text-amber-600",
  failed: "bg-destructive/10 text-destructive",
}

function formatDate(s: string) {
  return new Date(s).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })
}

interface Props {
  organizationId: string
}

// Owns its own owner-gating (invoices are only visible to the org owner —
// the API's own RBAC is what actually enforces this, this just avoids an
// empty/erroring fetch for members).
export function InvoicesTable({ organizationId }: Props) {
  const { t } = useTranslation()
  const { isOwner } = usePermissions()
  const [statusFilter, setStatusFilter] = useState("all")
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const { data, isLoading } = useInvoices(organizationId, isOwner)
  const { data: linksData } = usePaymentLinks(organizationId)
  const { data: paymentsData } = usePayments(organizationId)
  const allPayments = paymentsData?.data ?? []
  const paymentsByInvoice = new Map<string, typeof allPayments>()
  for (const p of allPayments) {
    paymentsByInvoice.set(p.invoice_id, [
      ...(paymentsByInvoice.get(p.invoice_id) ?? []),
      p,
    ])
  }
  const allInvoices = data?.data ?? []
  const invoices =
    statusFilter === "all"
      ? allInvoices
      : allInvoices.filter((inv) => inv.status === statusFilter)

  // A pending invoice with an already-open (unexpired, unpaid) link
  // shows "awaiting payment" instead of a fresh Pay button, until the
  // webhook confirms and flips the invoice to paid.
  const openLinkByInvoice = new Set(
    (linksData?.data ?? [])
      .filter((l) => l.status === "pending")
      .map((l) => l.invoice_id)
  )

  return (
    <Card id="billing-invoices" className="scroll-mt-6">
      <CardHeader>
        <CardTitle>{t("billing.invoices.title")}</CardTitle>
        {isOwner && !!allInvoices.length && (
          <CardAction>
            <Select
              value={statusFilter}
              onValueChange={(v) => setStatusFilter(v ?? "all")}
            >
              <SelectTrigger className="h-7 w-32 text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">
                  {t("billing.invoices.statusAll")}
                </SelectItem>
                <SelectItem value="pending">
                  {t("billing.invoices.statusPending")}
                </SelectItem>
                <SelectItem value="paid">
                  {t("billing.invoices.statusPaid")}
                </SelectItem>
                <SelectItem value="failed">
                  {t("billing.invoices.statusFailed")}
                </SelectItem>
                <SelectItem value="void">
                  {t("billing.invoices.statusVoid")}
                </SelectItem>
              </SelectContent>
            </Select>
          </CardAction>
        )}
      </CardHeader>
      <CardContent>
        {!isOwner ? (
          <p className="text-sm text-muted-foreground">
            {t("billing.invoicesOwnerOnlyNote")}
          </p>
        ) : isLoading ? (
          <div className="flex flex-col gap-2">
            {[1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-16 w-full" />
            ))}
          </div>
        ) : !allInvoices.length ? (
          <p className="text-sm text-muted-foreground">
            {t("billing.invoices.noInvoices")}
          </p>
        ) : (
          <div className="flex flex-col gap-3">
            {invoices.map((inv) => {
              const isActive = inv.status === "pending" || inv.status === "paid"
              // Backend only ever creates a payment link for a `pending`
              // invoice (`createPaymentLink` rejects anything else with
              // ErrInvoiceNotPayable) — `void` invoices are permanently
              // superseded (voided when a plan change issues a fresh
              // invoice, see `voidPendingInvoicesAndLinks`) and can never
              // regenerate.
              const canRegenerate = inv.status === "failed"
              const invPayments = paymentsByInvoice.get(inv.id) ?? []
              const isExpanded = expandedId === inv.id
              // Preview/Download only ever apply to an active invoice (a
              // real generated PDF exists) — expanding must stay possible
              // even with zero payment attempts yet, or those actions
              // would be unreachable for a freshly-created pending invoice.
              const expandable = isActive || invPayments.length > 0
              return (
                <div
                  key={inv.id}
                  className={cn(
                    "overflow-hidden rounded-lg border border-border",
                    !isActive && !canRegenerate && "opacity-50"
                  )}
                >
                  <div
                    className={cn(
                      "flex flex-wrap items-center justify-between gap-3 p-4",
                      expandable && "cursor-pointer"
                    )}
                    onClick={() =>
                      expandable && setExpandedId(isExpanded ? null : inv.id)
                    }
                  >
                    <div className="flex items-center gap-2">
                      {expandable && (
                        <IconChevronDown
                          className={cn(
                            "size-4 shrink-0 text-muted-foreground transition-transform",
                            isExpanded ? "rotate-180" : ""
                          )}
                        />
                      )}
                      <div>
                        <p className="font-mono text-xs text-muted-foreground">
                          {inv.invoice_number ?? "—"}
                        </p>
                        <p className="text-sm text-muted-foreground">
                          {formatDate(inv.created_at)}
                        </p>
                      </div>
                    </div>
                    <div className="flex items-center gap-3">
                      <span
                        className={cn(
                          "text-sm font-semibold",
                          canRegenerate && "line-through opacity-50"
                        )}
                      >
                        {formatMoney(inv.amount_cents, inv.currency)}
                      </span>
                      <span
                        className={cn(
                          "rounded-full px-2 py-0.5 text-xs font-medium capitalize",
                          STATUS_BADGE[inv.status] ?? ""
                        )}
                      >
                        {inv.status}
                      </span>
                      <div
                        className="flex items-center gap-1"
                        onClick={(e) => e.stopPropagation()}
                      >
                        {inv.status === "pending" && (
                          <PayButton
                            invoiceId={inv.id}
                            organizationId={organizationId}
                            amountCents={inv.amount_cents}
                            currency={inv.currency}
                            awaitingPayment={openLinkByInvoice.has(inv.id)}
                          />
                        )}
                        {canRegenerate && (
                          <RegenerateButton
                            invoiceId={inv.id}
                            organizationId={organizationId}
                          />
                        )}
                      </div>
                    </div>
                  </div>
                  {isExpanded && expandable && (
                    <div className="divide-y divide-border border-t border-border bg-muted/30">
                      {invPayments.map((p, idx) => {
                        const isLast = idx === invPayments.length - 1
                        return (
                          <div
                            key={p.id}
                            className="grid grid-cols-1 gap-4 p-4 sm:grid-cols-2"
                          >
                            <div>
                              <p className="text-xs font-medium text-muted-foreground uppercase">
                                {t("billing.payments.referenceCol")}
                              </p>
                              <p className="mt-1 font-mono text-sm">
                                {p.external_id ?? "—"}
                              </p>
                            </div>
                            <div>
                              <p className="text-xs font-medium text-muted-foreground uppercase">
                                {t("billing.payments.providerCol")}
                              </p>
                              <p className="mt-1 text-sm capitalize">
                                {p.provider}
                              </p>
                            </div>
                            <div>
                              <p className="text-xs font-medium text-muted-foreground uppercase">
                                {t("billing.payments.amountCol")}
                              </p>
                              <p className="mt-1 text-sm font-medium">
                                {formatMoney(p.amount_cents, p.currency)}
                              </p>
                            </div>
                            <div>
                              <p className="text-xs font-medium text-muted-foreground uppercase">
                                {t("billing.payments.statusCol")}
                              </p>
                              <p className="mt-1">
                                <span
                                  className={cn(
                                    "rounded-full px-2 py-0.5 text-xs font-medium capitalize",
                                    PAYMENT_STATUS_BADGE[p.status] ??
                                      "bg-muted text-muted-foreground"
                                  )}
                                >
                                  {p.status}
                                </span>
                              </p>
                            </div>
                            <div>
                              <p className="text-xs font-medium text-muted-foreground uppercase">
                                {t("billing.payments.dateCol")}
                              </p>
                              <p className="mt-1 text-sm">
                                {formatDate(p.paid_at)}
                              </p>
                            </div>
                            {isLast && isActive && (
                              <div className="col-span-full flex items-center justify-end gap-1">
                                <PreviewButton
                                  invoiceId={inv.id}
                                  organizationId={organizationId}
                                />
                                <PdfButton
                                  invoiceId={inv.id}
                                  organizationId={organizationId}
                                />
                              </div>
                            )}
                          </div>
                        )
                      })}
                      {!invPayments.length && isActive && (
                        <div className="flex items-center gap-1 p-4">
                          <PreviewButton
                            invoiceId={inv.id}
                            organizationId={organizationId}
                          />
                          <PdfButton
                            invoiceId={inv.id}
                            organizationId={organizationId}
                          />
                        </div>
                      )}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
