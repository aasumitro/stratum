import { useBlocker } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { ConfirmationDialog } from "@/components/shared/confirmation-dialog"

interface UnsavedChangesGuardProps {
  /** true while the form has edits that haven't been saved */
  isDirty: boolean
}

/**
 * Blocks in-app navigation away from a dirty form (route change or
 * browser tab close/refresh) and asks the caller to confirm before
 * discarding. Mount inside any form component and pass its own dirty-state
 * — no per-page wiring beyond that one prop.
 */
export function UnsavedChangesGuard({ isDirty }: UnsavedChangesGuardProps) {
  const { t } = useTranslation()
  const { status, proceed, reset } = useBlocker({
    shouldBlockFn: () => isDirty,
    enableBeforeUnload: () => isDirty,
    withResolver: true,
  })

  return (
    <ConfirmationDialog
      open={status === "blocked"}
      onOpenChange={(open) => !open && reset?.()}
      render={<span className="hidden" />}
      nativeButton={false}
      title={t("common.unsavedChangesTitle")}
      description={t("common.unsavedChangesDescription")}
      confirmLabel={t("common.unsavedChangesLeave")}
      cancelLabel={t("common.unsavedChangesStay")}
      onConfirm={() => proceed?.()}
    >
      {null}
    </ConfirmationDialog>
  )
}
