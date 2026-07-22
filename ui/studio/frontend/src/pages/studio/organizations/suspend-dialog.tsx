import { useState } from "react"
import type { OrganizationSummary } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { useSuspendOrganization } from "@/hooks/use-organization-ops"
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
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/ui"

export type StatusFilter = "" | "active" | "suspended" | "deleted"

export function statusBadge(status: string) {
  const map: Record<string, string> = {
    active:    "bg-green-500/10 text-green-700 dark:text-green-400 border-green-500/20",
    suspended: "bg-amber-500/10 text-amber-700 dark:text-amber-400 border-amber-500/20",
    deleted:   "bg-red-500/10 text-red-700 dark:text-red-400 border-red-500/20",
  }
  return (
    <span className={cn("inline-flex items-center px-2 py-0.5 rounded border text-xs font-medium", map[status] ?? "bg-muted text-muted-foreground")}>
      {status}
    </span>
  )
}

export function billingBadge(billing: string) {
  if (!billing) return <span className="text-muted-foreground text-xs">—</span>
  return <Badge variant="secondary" className="text-xs">{billing}</Badge>
}

export function SuspendDialog({
  organization,
  projectId,
  statusFilter,
  onClose,
}: {
  organization: OrganizationSummary
  projectId: string
  statusFilter: StatusFilter
  onClose: () => void
}) {
  const [reason, setReason] = useState("")
  const suspend = useSuspendOrganization(projectId, statusFilter)

  return (
    <AlertDialog open onOpenChange={(o) => !o && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Suspend organization?</AlertDialogTitle>
          <AlertDialogDescription className="space-y-3">
            <span className="block">
              This will suspend <strong>{organization.name}</strong> and block all member access until unsuspended.
            </span>
            <Input
              placeholder="Reason (optional)"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              autoFocus
            />
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel onClick={onClose}>Cancel</AlertDialogCancel>
          <AlertDialogAction
            className="bg-amber-600 hover:bg-amber-700 text-white"
            disabled={suspend.isPending}
            onClick={() =>
              suspend.mutate({ organizationID: organization.id, reason }, { onSuccess: onClose })
            }
          >
            {suspend.isPending ? "Suspending…" : "Suspend"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
