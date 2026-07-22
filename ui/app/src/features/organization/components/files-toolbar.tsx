import { useRef } from "react"
import { useTranslation } from "react-i18next"
import { IconUpload } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import { SearchBar } from "@/components/shared/search-bar"

interface Props {
  search: string
  onSearchChange: (value: string) => void
  searchAll: boolean
  onSearchAllChange: (value: boolean) => void
  onFilesSelected: (files: FileList) => void
}

export function FilesToolbar({
  search,
  onSearchChange,
  searchAll,
  onSearchAllChange,
  onFilesSelected,
}: Props) {
  const { t } = useTranslation()
  const inputRef = useRef<HTMLInputElement>(null)

  return (
    <div className="flex flex-wrap items-center gap-2">
      <SearchBar
        value={search}
        onChange={onSearchChange}
        placeholder={t("organization.files.searchPlaceholder")}
        className="w-56"
      />
      <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <Switch checked={searchAll} onCheckedChange={onSearchAllChange} />
        {t("organization.files.searchAll")}
      </label>
      <span className="flex-1" />
      <input
        ref={inputRef}
        type="file"
        multiple
        className="hidden"
        onChange={(e) => {
          if (e.target.files) onFilesSelected(e.target.files)
          e.target.value = ""
        }}
      />
      <Button onClick={() => inputRef.current?.click()}>
        <IconUpload data-icon="inline-start" />
        {t("organization.files.upload")}
      </Button>
    </div>
  )
}
