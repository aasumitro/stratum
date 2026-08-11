import { IconLoader2 } from "@tabler/icons-react"
import type { Project } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { useDeleteProject } from "@/hooks/use-projects"
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"

interface DeleteProjectDialogProps {
  project: Project | null
  onClose: () => void
}

export function DeleteProjectDialog({
  project,
  onClose,
}: DeleteProjectDialogProps) {
  const del = useDeleteProject()

  const handleDelete = async () => {
    if (!project) return
    await del.mutateAsync(project.id)
    onClose()
  }

  return (
    <AlertDialog
      open={project !== null}
      onOpenChange={(v: boolean) => !v && onClose()}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            Delete &ldquo;{project?.name}&rdquo;?
          </AlertDialogTitle>
          <AlertDialogDescription>
            This removes the project from Studio. It does not affect the Stratum
            deployment itself. This action cannot be undone.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={del.isPending}>Cancel</AlertDialogCancel>
          <Button
            variant="destructive"
            onClick={handleDelete}
            disabled={del.isPending}
          >
            {del.isPending ? (
              <IconLoader2 className="mr-2 size-4 animate-spin" />
            ) : null}
            Delete
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
