import { useTranslation } from "react-i18next"
import { IconDownload, IconDots, IconFile, IconX } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { API } from "@/lib/api/path"
import { formatBytes } from "@/lib/format"
import type { OrganizationFile } from "@/types/organization"
import type { UploadItem } from "@/features/organization/pages/files-page"

interface Props {
  organizationId: string
  files: OrganizationFile[]
  uploads: UploadItem[]
  isLoading: boolean
  isEmpty: boolean
  dragOver: boolean
  selected: Set<string>
  onToggleSelect: (fileId: string) => void
  onMove: (file: OrganizationFile) => void
  onDelete: (file: OrganizationFile) => void
}

export function FilesListTable({
  organizationId,
  files,
  uploads,
  isLoading,
  isEmpty,
  dragOver,
  selected,
  onToggleSelect,
  onMove,
  onDelete,
}: Props) {
  const { t } = useTranslation()
  return (
    <div className="rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-8" />
            <TableHead>{t("organization.files.columns.name")}</TableHead>
            <TableHead>{t("organization.files.columns.size")}</TableHead>
            <TableHead>{t("organization.files.columns.uploadedBy")}</TableHead>
            <TableHead>{t("organization.files.columns.date")}</TableHead>
            <TableHead className="hidden w-20 md:table-cell" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading ? (
            <TableRow>
              <TableCell
                colSpan={6}
                className="py-10 text-center text-muted-foreground"
              >
                {t("common.loading")}
              </TableCell>
            </TableRow>
          ) : isEmpty ? (
            <TableRow>
              <TableCell colSpan={6} className="py-2">
                <div
                  className={`m-2 flex flex-col items-center gap-2 rounded-xl border-2 border-dashed py-10 text-muted-foreground ${dragOver ? "border-primary bg-primary/5" : ""}`}
                >
                  <IconFile className="size-8 opacity-40" />
                  <p className="text-sm">{t("organization.files.empty")}</p>
                  <p className="text-xs">{t("organization.files.dropHint")}</p>
                </div>
              </TableCell>
            </TableRow>
          ) : (
            <>
              {files.map((file) => (
                <TableRow key={file.id}>
                  <TableCell>
                    <Checkbox
                      checked={selected.has(file.id)}
                      onCheckedChange={() => onToggleSelect(file.id)}
                    />
                  </TableCell>
                  <TableCell className="max-w-[200px] truncate font-medium">
                    {file.name}
                  </TableCell>
                  <TableCell>{formatBytes(file.size_bytes)}</TableCell>
                  <TableCell className="max-w-[140px] truncate text-sm text-muted-foreground">
                    {file.created_by}
                  </TableCell>
                  <TableCell className="text-sm text-muted-foreground">
                    {new Date(file.created_at).toLocaleDateString()}
                  </TableCell>
                  <TableCell className="hidden md:table-cell">
                    <div className="flex items-center gap-1">
                      <Button
                        variant="ghost"
                        size="icon"
                        title={t("organization.files.download")}
                        aria-label={t("organization.files.download")}
                        onClick={() =>
                          window.open(
                            API.files(organizationId, file.id, "download"),
                            "_blank"
                          )
                        }
                      >
                        <IconDownload className="size-4" />
                      </Button>
                      <DropdownMenu>
                        <DropdownMenuTrigger
                          render={
                            <Button
                              variant="ghost"
                              size="icon"
                              aria-label={t("organization.files.moreActions")}
                            />
                          }
                        >
                          <IconDots className="size-4" />
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem onClick={() => onMove(file)}>
                            {t("organization.files.move")}
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            variant="destructive"
                            onClick={() => onDelete(file)}
                          >
                            {t("organization.files.delete")}
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
              {uploads.map((u) => (
                <TableRow key={u.id}>
                  <TableCell />
                  <TableCell colSpan={4}>
                    <div className="flex items-center gap-2">
                      <span className="max-w-[160px] truncate text-sm">
                        {u.file.name}
                      </span>
                      {u.status === "uploading" ? (
                        <>
                          <div className="h-1.5 w-24 overflow-hidden rounded-full bg-muted">
                            <div
                              className="h-full bg-primary"
                              style={{ width: `${u.progress}%` }}
                            />
                          </div>
                          <span className="text-xs text-muted-foreground">
                            {t("organization.files.uploadingPercent", {
                              percent: u.progress,
                            })}
                          </span>
                          <button
                            onClick={() => u.controller.abort()}
                            title={t("common.cancel")}
                            aria-label={t("common.cancel")}
                          >
                            <IconX className="size-3.5 text-muted-foreground" />
                          </button>
                        </>
                      ) : u.status === "error" ? (
                        <span className="text-xs text-destructive">
                          {u.error}
                        </span>
                      ) : u.status === "cancelled" ? (
                        <span className="text-xs text-muted-foreground">
                          {t("organization.files.uploadCancelled")}
                        </span>
                      ) : (
                        <span className="text-xs text-emerald-600">
                          {t("organization.files.uploadDone")}
                        </span>
                      )}
                    </div>
                  </TableCell>
                  <TableCell />
                </TableRow>
              ))}
            </>
          )}
        </TableBody>
      </Table>
    </div>
  )
}
