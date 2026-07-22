import { useTranslation } from "react-i18next"
import { IconFolderPlus, IconTrash } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { FolderTree } from "@/features/organization/components/folder-tree"
import { FilesStorageBar } from "@/features/organization/components/files-storage-bar"
import type { Entitlement } from "@/types/billing"
import type { Folder } from "@/types/organization"

interface Props {
  storage: Entitlement | undefined
  folders: Folder[]
  activeFolderId: string | undefined
  onSelectFolder: (folderId: string | undefined) => void
  onDeleteFolder: (folderId: string) => void
  onNewFolder: () => void
  showTrash: boolean
  onToggleTrash: () => void
  trashCount: number
}

export function FilesSidebar({
  storage,
  folders,
  activeFolderId,
  onSelectFolder,
  onDeleteFolder,
  onNewFolder,
  showTrash,
  onToggleTrash,
  trashCount,
}: Props) {
  const { t } = useTranslation()
  return (
    <div className="flex w-48 shrink-0 flex-col gap-3">
      <FilesStorageBar storage={storage} />
      <FolderTree
        folders={folders}
        activeFolderId={showTrash ? "__trash__" : activeFolderId}
        onSelect={onSelectFolder}
        allLabel={t("organization.files.allFiles")}
        onDelete={onDeleteFolder}
      />
      <Button
        variant="ghost"
        size="sm"
        className="justify-start"
        onClick={onNewFolder}
      >
        <IconFolderPlus data-icon="inline-start" />
        {t("organization.files.newFolder")}
      </Button>
      <Button
        variant={showTrash ? "secondary" : "ghost"}
        size="sm"
        className="justify-start"
        onClick={onToggleTrash}
      >
        <IconTrash data-icon="inline-start" />
        {t("organization.files.trash")}
        {trashCount > 0 && (
          <span className="ml-auto text-xs text-muted-foreground">
            {trashCount}
          </span>
        )}
      </Button>
    </div>
  )
}
