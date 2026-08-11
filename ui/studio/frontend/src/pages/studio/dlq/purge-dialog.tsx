import { IconAlertTriangle } from "@tabler/icons-react"
import type { DLQInfo } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { usePurgeQueue } from "@/hooks/use-dlq"
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

interface PurgeDialogProps {
  queue: DLQInfo
  projectId: string
  onClose: () => void
}

export function PurgeDialog({ queue, projectId, onClose }: PurgeDialogProps) {
  const purge = usePurgeQueue(projectId)

  return (
    <AlertDialog
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle className="flex items-center gap-2">
            <IconAlertTriangle className="size-5 text-destructive" />
            Purge queue?
          </AlertDialogTitle>
          <AlertDialogDescription>
            All <span className="font-semibold">{queue.messages}</span> message
            {queue.messages !== 1 ? "s" : ""} in{" "}
            <span className="font-mono">{queue.name}</span> will be permanently
            deleted. This cannot be undone.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel onClick={onClose}>Cancel</AlertDialogCancel>
          <AlertDialogAction
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            onClick={() => {
              purge.mutate(queue.name)
              onClose()
            }}
          >
            Purge {queue.messages} message{queue.messages !== 1 ? "s" : ""}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
