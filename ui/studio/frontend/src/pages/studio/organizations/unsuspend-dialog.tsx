import type { OrganizationSummary } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { useUnsuspendOrganization } from "@/hooks/use-organization-ops"
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
import type { StatusFilter } from "./badges"

export function UnsuspendDialog({
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
  const unsuspend = useUnsuspendOrganization(projectId, statusFilter)

  return (
    <AlertDialog open onOpenChange={(o) => !o && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Restore organization?</AlertDialogTitle>
          <AlertDialogDescription>
            This will restore <strong>{organization.name}</strong> to active
            status and re-enable member access.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel onClick={onClose}>Cancel</AlertDialogCancel>
          <AlertDialogAction
            disabled={unsuspend.isPending}
            onClick={() =>
              unsuspend.mutate(organization.id, { onSuccess: onClose })
            }
          >
            {unsuspend.isPending ? "Restoring…" : "Restore"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
