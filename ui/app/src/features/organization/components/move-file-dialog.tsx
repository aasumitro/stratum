import { useState } from "react"
import { useTranslation } from "react-i18next"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { FolderTree } from "@/features/organization/components/folder-tree"
import type { Folder } from "@/types/organization"

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  folders: Folder[]
  pending: boolean
  onConfirm: (folderId: string | undefined) => void
}

export function MoveFileDialog({
  open,
  onOpenChange,
  folders,
  pending,
  onConfirm,
}: Props) {
  const { t } = useTranslation()
  const [target, setTarget] = useState<string | undefined>(undefined)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xs">
        <DialogHeader>
          <DialogTitle>{t("organization.files.moveTo")}</DialogTitle>
          <DialogDescription>
            {t("organization.files.moveToDescription")}
          </DialogDescription>
        </DialogHeader>
        <div className="max-h-64 overflow-y-auto rounded-lg border p-2">
          <FolderTree
            folders={folders}
            activeFolderId={target}
            onSelect={setTarget}
            allLabel={t("organization.files.rootFolder")}
          />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button disabled={pending} onClick={() => onConfirm(target)}>
            {t("organization.files.move")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
