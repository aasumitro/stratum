import { useState } from "react"
import { IconAlertCircle, IconAlertTriangle } from "@tabler/icons-react"
import type { AtRiskInvoice } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import {
  useAtRiskInvoices,
  useMarkAtRiskPaid,
  useVoidAtRiskInvoice,
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
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { cn } from "@/lib/ui"
import { formatCents } from "./utils"

function daysPendingChip(days: number, status: string) {
  if (status === "failed") {
    return (
      <span className="inline-flex items-center rounded border border-red-500/20 bg-red-500/10 px-2 py-0.5 text-xs font-medium text-red-700 dark:text-red-400">
        failed
      </span>
    )
  }
  const color =
    days > 30
      ? "bg-red-500/10 text-red-700 dark:text-red-400 border-red-500/20"
      : "bg-amber-500/10 text-amber-700 dark:text-amber-400 border-amber-500/20"
  return (
    <span
      className={cn(
        "inline-flex items-center rounded border px-2 py-0.5 text-xs font-medium",
        color
      )}
    >
      {days}d pending
    </span>
  )
}

export function AtRiskTab({ projectId }: { projectId: string }) {
  const {
    data: invoices,
    isLoading,
    error,
    refetch,
  } = useAtRiskInvoices(projectId)
  const markPaid = useMarkAtRiskPaid(projectId)
  const voidInv = useVoidAtRiskInvoice(projectId)
  const [confirmAction, setConfirmAction] = useState<{
    type: "paid" | "void"
    invoice: AtRiskInvoice
  } | null>(null)

  return (
    <div className="space-y-4 p-6">
      {isLoading ? (
        <div className="divide-y rounded-md border">
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="px-4 py-3">
              <Skeleton className="h-4 w-full" />
            </div>
          ))}
        </div>
      ) : error ? (
        <div className="flex flex-col items-center gap-2 py-12 text-sm text-muted-foreground">
          <span>Failed to load at-risk invoices</span>
          <Button variant="outline" size="sm" onClick={() => refetch()}>
            Try again
          </Button>
        </div>
      ) : !invoices || invoices.length === 0 ? (
        <div className="flex flex-col items-center gap-3 py-16 text-muted-foreground">
          <IconAlertCircle className="size-8 opacity-30" />
          <p className="text-sm">No at-risk invoices</p>
          <p className="max-w-xs text-center text-xs">
            Pending invoices older than 7 days and failed invoices will appear
            here.
          </p>
        </div>
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Invoice</TableHead>
                <TableHead>Organization</TableHead>
                <TableHead className="text-right">Amount</TableHead>
                <TableHead>Age</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {invoices.map((inv) => (
                <TableRow key={inv.invoice_id}>
                  <TableCell className="font-mono text-xs text-muted-foreground">
                    {inv.invoice_id.slice(0, 8)}…
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-col gap-0.5">
                      <span className="text-sm font-medium">
                        {inv.organization_name}
                      </span>
                      <span className="font-mono text-xs text-muted-foreground">
                        /{inv.organization_slug}
                      </span>
                    </div>
                  </TableCell>
                  <TableCell className="text-right text-sm tabular-nums">
                    {formatCents(inv.amount_cents, inv.currency)}
                  </TableCell>
                  <TableCell>
                    {daysPendingChip(inv.days_pending, inv.status)}
                  </TableCell>
                  <TableCell className="text-right">
                    {inv.status === "pending" && (
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="sm"
                          className="h-7 px-2 text-xs"
                          disabled={markPaid.isPending}
                          onClick={() =>
                            setConfirmAction({ type: "paid", invoice: inv })
                          }
                        >
                          Mark paid
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          className="h-7 px-2 text-xs text-destructive hover:text-destructive"
                          disabled={voidInv.isPending}
                          onClick={() =>
                            setConfirmAction({ type: "void", invoice: inv })
                          }
                        >
                          Void
                        </Button>
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <AlertDialog
        open={!!confirmAction}
        onOpenChange={(o) => {
          if (!o) setConfirmAction(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle className="flex items-center gap-2">
              <IconAlertTriangle className="size-5 text-destructive" />
              {confirmAction?.type === "paid"
                ? "Mark invoice as paid?"
                : "Void invoice?"}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {confirmAction?.type === "paid"
                ? "This will update the invoice status to paid directly in the database. No payment provider notification is sent."
                : "This will void the invoice. This action cannot be undone."}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => setConfirmAction(null)}>
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              className={
                confirmAction?.type === "void"
                  ? "bg-destructive text-destructive-foreground hover:bg-destructive/90"
                  : ""
              }
              onClick={() => {
                if (!confirmAction) return
                if (confirmAction.type === "paid") {
                  markPaid.mutate(confirmAction.invoice.invoice_id)
                } else {
                  voidInv.mutate(confirmAction.invoice.invoice_id)
                }
                setConfirmAction(null)
              }}
            >
              {confirmAction?.type === "paid" ? "Mark as paid" : "Void invoice"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
