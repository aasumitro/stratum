import { useState } from "react"
import { IconAlertTriangle, IconCash } from "@tabler/icons-react"
import type { PaymentToReview } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import {
  useMarkPaymentRefunded,
  usePaymentsToReview,
} from "@/hooks/use-support"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { formatDate } from "../components/table-utils"
import { formatCents } from "./utils"

// Money a provider took that settled nothing: a payment on a void or failed
// invoice, or a second payment of one invoice. The app has no refund path, so
// the operator refunds at the provider and then records it here.
export function PaymentsReviewSection({ projectId }: { projectId: string }) {
  const { data: payments } = usePaymentsToReview(projectId)
  const markRefunded = useMarkPaymentRefunded(projectId)
  const [confirm, setConfirm] = useState<PaymentToReview | null>(null)

  if (!payments || payments.length === 0) return null

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <IconCash className="size-4 text-red-500" />
        <div>
          <h2 className="text-sm font-semibold">Payments to review</h2>
          <p className="text-xs text-muted-foreground">
            Paid at the provider but applied to nothing. Refund each one at the
            provider, then mark it refunded.
          </p>
        </div>
      </div>
      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Organization</TableHead>
              <TableHead>Invoice</TableHead>
              <TableHead>Provider</TableHead>
              <TableHead className="text-right">Amount</TableHead>
              <TableHead>Paid</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {payments.map((p) => (
              <TableRow key={p.payment_id}>
                <TableCell>
                  <div className="flex flex-col gap-0.5">
                    <span className="text-sm font-medium">
                      {p.organization_name || "Deleted organization"}
                    </span>
                    <span className="font-mono text-xs text-muted-foreground">
                      {p.organization_slug
                        ? `/${p.organization_slug}`
                        : p.organization_id}
                    </span>
                  </div>
                </TableCell>
                <TableCell className="font-mono text-xs text-muted-foreground">
                  {p.invoice_id.slice(0, 8)}… · {p.invoice_status}
                </TableCell>
                <TableCell className="font-mono text-xs text-muted-foreground">
                  {p.provider} {p.external_id}
                </TableCell>
                <TableCell className="text-right text-sm tabular-nums">
                  {formatCents(p.amount_cents, p.currency)}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {formatDate(p.paid_at)}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 px-2 text-xs"
                    disabled={markRefunded.isPending}
                    onClick={() => setConfirm(p)}
                  >
                    Mark refunded
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      <AlertDialog
        open={!!confirm}
        onOpenChange={(o) => {
          if (!o) setConfirm(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle className="flex items-center gap-2">
              <IconAlertTriangle className="size-5 text-destructive" />
              Mark payment refunded?
            </AlertDialogTitle>
            <AlertDialogDescription>
              Studio does not refund anything. Refund this payment at{" "}
              {confirm?.provider} first; this only records that you did.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => setConfirm(null)}>
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (confirm) markRefunded.mutate(confirm.payment_id)
                setConfirm(null)
              }}
            >
              Mark refunded
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
