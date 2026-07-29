import { toast } from "sonner"
import i18n from "@/lib/i18n"

/** Success toast carrying an Undo action, for reversible destructive ops. */
export function toastWithUndo(
  message: string,
  onUndo: () => void,
  undoLabel?: string
) {
  toast.success(message, {
    action: { label: undoLabel ?? i18n.t("common.undo"), onClick: onUndo },
  })
}
