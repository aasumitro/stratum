import { useState } from "react"
import {
  IconAlertTriangle,
  IconChevronDown,
  IconChevronRight,
} from "@tabler/icons-react"
import type {
  UserInvoice,
  UserOrganization,
} from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import {
  useMarkInvoicePaid,
  useOrganizationInvoices,
  useVoidInvoice,
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
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { formatCents, formatDate } from "./utils"

interface InvoiceSectionProps {
  projectId: string
  organization: UserOrganization
}

export function InvoiceSection({
  projectId,
  organization,
}: InvoiceSectionProps) {
  const [expanded, setExpanded] = useState(false)
  const { data: invoices, isLoading } = useOrganizationInvoices(
    projectId,
    organization.id,
    expanded
  )
  const markPaid = useMarkInvoicePaid(projectId, organization.id)
  const voidInv = useVoidInvoice(projectId, organization.id)
  const [confirmAction, setConfirmAction] = useState<{
    type: "paid" | "void"
    invoice: UserInvoice
  } | null>(null)

  return (
    <div className="mt-2 border-t">
      <button
        onClick={() => setExpanded((v) => !v)}
        className="flex items-center gap-1.5 px-2 py-1.5 text-xs text-muted-foreground transition-colors hover:text-foreground"
      >
        {expanded ? (
          <IconChevronDown className="size-3" />
        ) : (
          <IconChevronRight className="size-3" />
        )}
        Invoices
      </button>

      {expanded && (
        <div className="px-2 pb-3">
          {isLoading ? (
            <p className="py-2 text-xs text-muted-foreground">Loading…</p>
          ) : !invoices || invoices.length === 0 ? (
            <p className="py-2 text-xs text-muted-foreground">No invoices</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="h-7 text-xs">ID</TableHead>
                  <TableHead className="h-7 text-xs">Amount</TableHead>
                  <TableHead className="h-7 text-xs">Status</TableHead>
                  <TableHead className="h-7 text-xs">Date</TableHead>
                  <TableHead className="h-7 text-right text-xs">
                    Actions
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {invoices.map((inv: UserInvoice) => (
                  <TableRow key={inv.id}>
                    <TableCell className="py-1.5 font-mono text-xs">
                      {inv.id.slice(0, 8)}…
                    </TableCell>
                    <TableCell className="py-1.5 text-xs tabular-nums">
                      {formatCents(inv.amount_cents, inv.currency)}
                    </TableCell>
                    <TableCell className="py-1.5">
                      <Badge
                        variant={
                          inv.status === "paid"
                            ? "default"
                            : inv.status === "pending"
                              ? "secondary"
                              : "outline"
                        }
                        className="text-xs"
                      >
                        {inv.status}
                      </Badge>
                    </TableCell>
                    <TableCell className="py-1.5 text-xs text-muted-foreground">
                      {formatDate(inv.created_at)}
                    </TableCell>
                    <TableCell className="py-1.5 text-right">
                      {inv.status === "pending" && (
                        <div className="flex items-center justify-end gap-1">
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-6 px-2 text-xs"
                            onClick={() =>
                              setConfirmAction({ type: "paid", invoice: inv })
                            }
                          >
                            Mark paid
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-6 px-2 text-xs text-destructive hover:text-destructive"
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
          )}
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
                  markPaid.mutate(confirmAction.invoice.id)
                } else {
                  voidInv.mutate(confirmAction.invoice.id)
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
