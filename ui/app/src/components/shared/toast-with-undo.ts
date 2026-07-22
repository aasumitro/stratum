import { toast } from "sonner"

/** Success toast carrying an Undo action, for reversible destructive ops. */
export function toastWithUndo(
  message: string,
  onUndo: () => void,
  undoLabel = "Undo"
) {
  toast.success(message, {
    action: { label: undoLabel, onClick: onUndo },
  })
}
