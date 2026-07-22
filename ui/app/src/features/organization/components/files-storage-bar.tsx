import { useTranslation } from "react-i18next"
import { formatBytes } from "@/lib/format"
import type { Entitlement } from "@/types/billing"

interface Props {
  storage: Entitlement | undefined
}

export function FilesStorageBar({ storage }: Props) {
  const { t } = useTranslation()
  if (!storage) return null
  return (
    <div className="flex flex-col gap-1">
      <span className="text-xs font-medium text-muted-foreground">
        {t("organization.files.storage")}
      </span>
      {storage.limit !== undefined && storage.limit >= 0 ? (
        <>
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
            <div
              className="h-full bg-primary"
              style={{
                width: `${Math.min(100, ((storage.current ?? 0) / storage.limit) * 100)}%`,
              }}
            />
          </div>
          <span className="text-xs text-muted-foreground">
            {formatBytes(storage.current ?? 0)} {t("organization.files.of")}{" "}
            {formatBytes(storage.limit)}
          </span>
        </>
      ) : (
        <span className="text-xs text-muted-foreground">
          {formatBytes(storage.current ?? 0)}
        </span>
      )}
    </div>
  )
}
