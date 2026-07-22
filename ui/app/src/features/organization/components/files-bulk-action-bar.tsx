import { useTranslation } from "react-i18next"
import { IconX } from "@tabler/icons-react"

interface Props {
  count: number
  onMove: () => void
  onDownload: () => void
  onDelete: () => void
  deleting: boolean
  onClear: () => void
}

export function FilesBulkActionBar({
  count,
  onMove,
  onDownload,
  onDelete,
  deleting,
  onClear,
}: Props) {
  const { t } = useTranslation()
  return (
    <div className="flex items-center gap-3 rounded-lg bg-foreground px-3 py-2 text-sm text-background">
      <span>{t("organization.files.selectedCount", { count })}</span>
      <button className="underline" onClick={onMove}>
        {t("organization.files.move")}
      </button>
      <button className="underline" onClick={onDownload}>
        {t("organization.files.download")}
      </button>
      <button
        className="text-red-300 underline"
        disabled={deleting}
        onClick={onDelete}
      >
        {t("common.delete")}
      </button>
      <button className="ml-auto" onClick={onClear}>
        <IconX className="size-4" />
      </button>
    </div>
  )
}
