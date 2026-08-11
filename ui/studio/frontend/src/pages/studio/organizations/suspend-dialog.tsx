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
import { Input } from "@/components/ui/input"
import type { StatusFilter } from "./badges"

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
              This will suspend <strong>{organization.name}</strong> and block
              all member access until unsuspended.
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
            className="bg-amber-600 text-white hover:bg-amber-700"
            disabled={suspend.isPending}
            onClick={() =>
              suspend.mutate(
                { organizationID: organization.id, reason },
                { onSuccess: onClose }
              )
            }
          >
            {suspend.isPending ? "Suspending…" : "Suspend"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
