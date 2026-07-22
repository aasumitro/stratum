import { Fragment, useState } from "react"
import { useTranslation } from "react-i18next"
import { IconChevronDown, IconChevronRight } from "@tabler/icons-react"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { PdfButton } from "@/features/billing/components/pdf-button"
import { PayButton } from "@/features/billing/components/pay-button"
import { RegenerateButton } from "@/features/billing/components/regenerate-button"
import {
  useInvoices,
  usePaymentLinks,
  usePayments,
} from "@/features/billing/hooks"
import { formatMoney } from "@/lib/format"

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

export function InvoicesTable({ organizationId }: Props) {
  const { t } = useTranslation()
  const [statusFilter, setStatusFilter] = useState("all")
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const { data, isLoading } = useInvoices(organizationId)
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

  if (isLoading) {
    return (
      <div className="flex flex-col gap-2">
        {[1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-12 w-full" />
        ))}
      </div>
    )
  }

  if (!allInvoices.length) {
    return (
      <p className="text-sm text-muted-foreground">
        {t("billing.invoices.noInvoices")}
      </p>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-end">
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
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead />
            <TableHead>{t("billing.invoices.numberCol")}</TableHead>
            <TableHead>{t("billing.invoices.dateCol")}</TableHead>
            <TableHead>{t("billing.invoices.amountCol")}</TableHead>
            <TableHead>{t("billing.invoices.statusCol")}</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {invoices.map((inv) => {
            const isActive = inv.status === "pending" || inv.status === "paid"
            // Backend only ever creates a payment link for a `pending`
            // invoice (`createPaymentLink` rejects anything else with
            // ErrInvoiceNotPayable) — `void` invoices are permanently
            // superseded (voided when a plan change issues a fresh invoice,
            // see `voidPendingInvoicesAndLinks`) and can never regenerate.
            const canRegenerate = inv.status === "failed"
            const invPayments = paymentsByInvoice.get(inv.id) ?? []
            const isExpanded = expandedId === inv.id
            return (
              <Fragment key={inv.id}>
                <TableRow
                  className={
                    (!isActive && !canRegenerate ? "opacity-50 " : "") +
                    (invPayments.length ? "cursor-pointer" : "")
                  }
                  onClick={() =>
                    invPayments.length &&
                    setExpandedId(isExpanded ? null : inv.id)
                  }
                >
                  <TableCell className="w-6">
                    {invPayments.length > 0 &&
                      (isExpanded ? (
                        <IconChevronDown className="size-4 text-muted-foreground" />
                      ) : (
                        <IconChevronRight className="size-4 text-muted-foreground" />
                      ))}
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">
                    {inv.invoice_number ?? "—"}
                  </TableCell>
                  <TableCell className="text-sm">
                    {formatDate(inv.created_at)}
                  </TableCell>
                  <TableCell
                    className={`text-sm font-medium ${canRegenerate ? "line-through opacity-50" : ""}`}
                  >
                    {formatMoney(inv.amount_cents, inv.currency)}
                  </TableCell>
                  <TableCell>
                    <span
                      className={`rounded-full px-2 py-0.5 text-xs font-medium capitalize ${STATUS_BADGE[inv.status] ?? ""}`}
                    >
                      {inv.status}
                    </span>
                  </TableCell>
                  <TableCell className="text-right">
                    <div
                      className="flex items-center justify-end gap-1"
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
                      {isActive && (
                        <PdfButton
                          invoiceId={inv.id}
                          organizationId={organizationId}
                        />
                      )}
                    </div>
                  </TableCell>
                </TableRow>
                {isExpanded && invPayments.length > 0 && (
                  <TableRow key={`${inv.id}-payments`}>
                    <TableCell colSpan={6} className="bg-muted/30 p-0">
                      <Table>
                        <TableHeader>
                          <TableRow>
                            <TableHead className="pl-8">
                              {t("billing.payments.referenceCol")}
                            </TableHead>
                            <TableHead>
                              {t("billing.payments.providerCol")}
                            </TableHead>
                            <TableHead>
                              {t("billing.payments.amountCol")}
                            </TableHead>
                            <TableHead>
                              {t("billing.payments.statusCol")}
                            </TableHead>
                            <TableHead>
                              {t("billing.payments.dateCol")}
                            </TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {invPayments.map((p) => (
                            <TableRow key={p.id}>
                              <TableCell className="pl-8 font-mono text-xs text-muted-foreground">
                                {p.external_id ?? "—"}
                              </TableCell>
                              <TableCell className="text-sm capitalize">
                                {p.provider}
                              </TableCell>
                              <TableCell className="text-sm font-medium">
                                {formatMoney(p.amount_cents, p.currency)}
                              </TableCell>
                              <TableCell>
                                <span
                                  className={`rounded-full px-2 py-0.5 text-xs font-medium capitalize ${PAYMENT_STATUS_BADGE[p.status] ?? "bg-muted text-muted-foreground"}`}
                                >
                                  {p.status}
                                </span>
                              </TableCell>
                              <TableCell className="text-sm text-muted-foreground">
                                {formatDate(p.paid_at)}
                              </TableCell>
                            </TableRow>
                          ))}
                        </TableBody>
                      </Table>
                    </TableCell>
                  </TableRow>
                )}
              </Fragment>
            )
          })}
        </TableBody>
      </Table>
    </div>
  )
}
