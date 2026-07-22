import { useTranslation } from "react-i18next"
import { IconArrowBackUp, IconTrash } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { formatBytes } from "@/lib/format"
import type { OrganizationFile } from "@/types/organization"

interface Props {
  trashFiles: OrganizationFile[]
  onRestore: (fileId: string) => void
  onPurge: (fileId: string) => void
}

export function FilesTrashTable({ trashFiles, onRestore, onPurge }: Props) {
  const { t } = useTranslation()
  return (
    <div className="rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("organization.files.columns.name")}</TableHead>
            <TableHead>{t("organization.files.columns.size")}</TableHead>
            <TableHead>{t("organization.files.columns.uploadedBy")}</TableHead>
            <TableHead>{t("organization.files.columns.date")}</TableHead>
            <TableHead className="hidden w-20 md:table-cell" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {trashFiles.length === 0 ? (
            <TableRow>
              <TableCell
                colSpan={5}
                className="py-10 text-center text-muted-foreground"
              >
                {t("organization.files.trashEmpty")}
              </TableCell>
            </TableRow>
          ) : (
            trashFiles.map((file) => (
              <TableRow key={file.id}>
                <TableCell className="max-w-[200px] truncate font-medium">
                  {file.name}
                </TableCell>
                <TableCell>{formatBytes(file.size_bytes)}</TableCell>
                <TableCell className="max-w-[140px] truncate text-sm text-muted-foreground">
                  {file.created_by}
                </TableCell>
                <TableCell className="text-sm text-muted-foreground">
                  {file.deleted_at &&
                    new Date(file.deleted_at).toLocaleDateString()}
                </TableCell>
                <TableCell>
                  <div className="flex items-center gap-1">
                    <Button
                      variant="ghost"
                      size="icon"
                      title={t("organization.files.restore")}
                      aria-label={t("organization.files.restore")}
                      onClick={() => onRestore(file.id)}
                    >
                      <IconArrowBackUp className="size-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      title={t("organization.files.deleteForever")}
                      aria-label={t("organization.files.deleteForever")}
                      onClick={() => onPurge(file.id)}
                    >
                      <IconTrash className="size-4 text-destructive" />
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))
          )}
        </TableBody>
      </Table>
    </div>
  )
}
