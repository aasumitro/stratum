import { useMemo, useState } from "react"
import { useParams } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { ConfirmationDialog } from "@/components/shared/confirmation-dialog"
import { FilesSidebar } from "@/features/organization/components/files-sidebar"
import { FilesBreadcrumb } from "@/features/organization/components/files-breadcrumb"
import { FilesToolbar } from "@/features/organization/components/files-toolbar"
import { FilesBulkActionBar } from "@/features/organization/components/files-bulk-action-bar"
import { FilesListTable } from "@/features/organization/components/files-list-table"
import { FilesTrashTable } from "@/features/organization/components/files-trash-table"
import { FilesNewFolderDialog } from "@/features/organization/components/files-new-folder-dialog"
import { MoveFileDialog } from "@/features/organization/components/move-file-dialog"
import {
  useOrganizationFiles,
  useOrganizationFolders,
  useOrganizationTrash,
  useCreateFolder,
  useDeleteFolder,
  useMoveFile,
  useDeleteOrganizationFile,
  useBulkDeleteFiles,
  useRestoreFile,
  usePurgeFile,
  uploadOrganizationFile,
} from "@/features/organization/hooks"
import { useBillingFeatures } from "@/features/billing/hooks"
import { API } from "@/lib/api/path"
import { useQueryClient } from "@tanstack/react-query"
import type { OrganizationFile } from "@/types/organization"

const MAX_FILE_SIZE = 50 * 1024 * 1024

export interface UploadItem {
  id: string
  file: File
  progress: number
  status: "uploading" | "done" | "error" | "cancelled"
  error?: string
  controller: AbortController
}

// Folder tree + breadcrumb, bulk-select action bar, per-row upload
// progress with cancel, drag-drop-anywhere, 30-day trash. Was a flat,
// single-folder file list with a bare single-file input before this pass.
export function FilesPage() {
  const { t } = useTranslation()
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }
  const queryClient = useQueryClient()

  const [folderId, setFolderId] = useState<string | undefined>(undefined)
  const [search, setSearch] = useState("")
  const [searchAll, setSearchAll] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [showTrash, setShowTrash] = useState(false)
  const [toDelete, setToDelete] = useState<OrganizationFile | null>(null)
  const [moveTarget, setMoveTarget] = useState<OrganizationFile[] | null>(null)
  const [newFolderOpen, setNewFolderOpen] = useState(false)
  const [newFolderName, setNewFolderName] = useState("")
  const [uploads, setUploads] = useState<UploadItem[]>([])
  const [dragOver, setDragOver] = useState(false)

  const { data, isLoading } = useOrganizationFiles(
    organizationId,
    folderId,
    search || undefined,
    searchAll,
    undefined,
    !showTrash
  )
  const { data: trashData } = useOrganizationTrash(organizationId, showTrash)
  const { data: foldersData } = useOrganizationFolders(organizationId)
  const { data: entitlementsData } = useBillingFeatures(organizationId)

  const files = data?.data?.items ?? []
  const trashFiles = trashData?.data ?? []
  const folders = useMemo(() => foldersData?.data ?? [], [foldersData])
  const storage = (entitlementsData?.data ?? []).find(
    (e) => e.feature_id === "storage" || e.feature_id === "storage_bytes"
  )

  const { mutate: createFolder, isPending: creatingFolder } =
    useCreateFolder(organizationId)
  const { mutate: deleteFolder } = useDeleteFolder(organizationId)
  const { mutate: moveFile, isPending: moving } = useMoveFile(organizationId)
  const { mutate: deleteFile } = useDeleteOrganizationFile(organizationId)
  const { mutate: bulkDelete, isPending: bulkDeleting } =
    useBulkDeleteFiles(organizationId)
  const { mutate: restoreFile } = useRestoreFile(organizationId)
  const { mutate: purgeFile } = usePurgeFile(organizationId)

  const breadcrumb = useMemo(() => {
    const chain: { id: string | undefined; name: string }[] = [
      { id: undefined, name: t("organization.files.rootFolder") },
    ]
    let current = folders.find((f) => f.id === folderId)
    const trail: { id: string; name: string }[] = []
    while (current) {
      trail.unshift({ id: current.id, name: current.name })
      current = folders.find((f) => f.id === current!.parent_folder_id)
    }
    return [...chain, ...trail]
  }, [folders, folderId, t])

  function startUploads(fileList: FileList | File[]) {
    const list = Array.from(fileList)
    for (const file of list) {
      if (file.size > MAX_FILE_SIZE) {
        toast.error(t("organization.files.tooLarge", { name: file.name }))
        continue
      }
      const controller = new AbortController()
      const id = `${file.name}-${Date.now()}-${Math.random()}`
      setUploads((prev) => [
        ...prev,
        { id, file, progress: 0, status: "uploading", controller },
      ])
      uploadOrganizationFile(
        organizationId,
        file,
        folderId,
        (progress) =>
          setUploads((prev) =>
            prev.map((u) => (u.id === id ? { ...u, progress } : u))
          ),
        controller.signal
      )
        .then(() => {
          setUploads((prev) =>
            prev.map((u) => (u.id === id ? { ...u, status: "done" } : u))
          )
          void queryClient.invalidateQueries({
            queryKey: ["organizations", organizationId, "files"],
          })
          setTimeout(
            () => setUploads((prev) => prev.filter((u) => u.id !== id)),
            2000
          )
        })
        .catch((err) => {
          const code = err?.status?.code
          setUploads((prev) =>
            prev.map((u) =>
              u.id === id
                ? {
                    ...u,
                    status:
                      err?.name === "CanceledError" ||
                      err?.code === "ERR_CANCELED"
                        ? "cancelled"
                        : "error",
                    error:
                      code === "STORAGE_LIMIT_REACHED"
                        ? t("organization.files.quotaExceeded")
                        : t("organization.files.uploadFailed"),
                  }
                : u
            )
          )
        })
    }
  }

  function toggleSelect(id: string) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const isEmpty = !isLoading && files.length === 0 && uploads.length === 0

  return (
    <div
      className="flex gap-6"
      onDragOver={(e) => {
        e.preventDefault()
        setDragOver(true)
      }}
      onDragLeave={() => setDragOver(false)}
      onDrop={(e) => {
        e.preventDefault()
        setDragOver(false)
        if (e.dataTransfer.files.length) startUploads(e.dataTransfer.files)
      }}
    >
      <FilesSidebar
        storage={storage}
        folders={folders}
        activeFolderId={folderId}
        onSelectFolder={(id) => {
          setShowTrash(false)
          setFolderId(id)
          setSelected(new Set())
        }}
        onDeleteFolder={(id) => {
          deleteFolder(id, {
            onSuccess: () => {
              if (folderId === id) setFolderId(undefined)
            },
          })
        }}
        onNewFolder={() => setNewFolderOpen(true)}
        showTrash={showTrash}
        onToggleTrash={() => setShowTrash((v) => !v)}
        trashCount={trashFiles.length}
      />

      {/* Main content */}
      <div className="flex min-w-0 flex-1 flex-col gap-4">
        {!showTrash && (
          <>
            <FilesBreadcrumb items={breadcrumb} onNavigate={setFolderId} />

            <FilesToolbar
              search={search}
              onSearchChange={setSearch}
              searchAll={searchAll}
              onSearchAllChange={setSearchAll}
              onFilesSelected={startUploads}
            />

            {selected.size > 0 && (
              <FilesBulkActionBar
                count={selected.size}
                onMove={() =>
                  setMoveTarget(files.filter((f) => selected.has(f.id)))
                }
                onDownload={() => {
                  for (const f of files) {
                    if (selected.has(f.id)) {
                      window.open(
                        API.files(organizationId, f.id, "download"),
                        "_blank"
                      )
                    }
                  }
                }}
                onDelete={() =>
                  bulkDelete(
                    { file_ids: Array.from(selected) },
                    { onSuccess: () => setSelected(new Set()) }
                  )
                }
                deleting={bulkDeleting}
                onClear={() => setSelected(new Set())}
              />
            )}
          </>
        )}

        {showTrash ? (
          <FilesTrashTable
            trashFiles={trashFiles}
            onRestore={(id) => restoreFile(id)}
            onPurge={(id) => purgeFile(id)}
          />
        ) : (
          <FilesListTable
            organizationId={organizationId}
            files={files}
            uploads={uploads}
            isLoading={isLoading}
            isEmpty={isEmpty}
            dragOver={dragOver}
            selected={selected}
            onToggleSelect={toggleSelect}
            onMove={(file) => setMoveTarget([file])}
            onDelete={(file) => setToDelete(file)}
          />
        )}
      </div>

      <ConfirmationDialog
        render={<span className="hidden" />}
        nativeButton={false}
        open={!!toDelete}
        onOpenChange={(open) => !open && setToDelete(null)}
        title={t("organization.files.deleteTitle")}
        description={t("organization.files.deleteConfirm", {
          name: toDelete?.name,
        })}
        confirmLabel={t("common.delete")}
        onConfirm={() => {
          if (toDelete) deleteFile(toDelete.id)
          setToDelete(null)
        }}
      >
        {null}
      </ConfirmationDialog>

      <MoveFileDialog
        open={!!moveTarget}
        onOpenChange={(open) => !open && setMoveTarget(null)}
        folders={folders}
        pending={moving}
        onConfirm={(target) => {
          if (!moveTarget) return
          for (const f of moveTarget) {
            moveFile({ fileId: f.id, folder_id: target ?? null })
          }
          setMoveTarget(null)
          setSelected(new Set())
        }}
      />

      <FilesNewFolderDialog
        open={newFolderOpen}
        onOpenChange={setNewFolderOpen}
        name={newFolderName}
        onNameChange={setNewFolderName}
        pending={creatingFolder}
        onCreate={() =>
          createFolder(
            { name: newFolderName.trim(), parent_folder_id: folderId },
            {
              onSuccess: () => {
                setNewFolderOpen(false)
                setNewFolderName("")
              },
            }
          )
        }
      />
    </div>
  )
}
