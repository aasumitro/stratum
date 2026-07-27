import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconDeviceDesktop, IconDeviceMobile } from "@tabler/icons-react"
import { Skeleton } from "@/components/ui/skeleton"
import { DataTablePagination } from "@/components/shared/pagination"
import { useCursorAccumulator } from "@/lib/api/use-cursor-accumulator"
import { useSessions } from "@/features/account/hooks"
import { timeAgo, describeDevice } from "@/lib/format"
import type { LoginEvent } from "@/types/account"

export function SessionsTable() {
  const { t } = useTranslation()
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const { data, isLoading, isFetching } = useSessions(cursor)
  const { items: sessions, nextCursor } = useCursorAccumulator<LoginEvent>(
    data,
    cursor
  )

  return (
    <div className="flex flex-col gap-3">
      {isLoading ? (
        Array.from({ length: 3 }).map((_, i) => (
          <div
            key={i}
            className="flex items-center justify-between rounded-md border p-3"
          >
            <div className="flex items-center gap-2.5">
              <Skeleton className="size-4 rounded-full" />
              <div className="flex flex-col gap-1.5">
                <Skeleton className="h-3.5 w-32" />
                <Skeleton className="h-3 w-24" />
              </div>
            </div>
            <Skeleton className="h-3 w-16" />
          </div>
        ))
      ) : sessions.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          {t("settings.sessions.empty")}
        </p>
      ) : (
        sessions.map((s) => {
          const device = describeDevice(s.user_agent)
          const DeviceIcon = device.isMobile
            ? IconDeviceMobile
            : IconDeviceDesktop
          return (
            <div
              key={s.id}
              className="flex items-center justify-between gap-4 rounded-md border p-3"
            >
              <div className="flex min-w-0 items-center gap-2.5">
                <DeviceIcon className="size-4 shrink-0 text-muted-foreground" />
                <div className="min-w-0">
                  <p
                    className="truncate text-sm font-medium"
                    title={s.user_agent}
                  >
                    {device.label}
                  </p>
                  <p className="truncate font-mono text-xs text-muted-foreground">
                    {s.ip_address || "—"}
                  </p>
                </div>
              </div>
              <span className="shrink-0 text-xs whitespace-nowrap text-muted-foreground">
                {timeAgo(s.created_at)}
              </span>
            </div>
          )
        })
      )}
      <DataTablePagination
        mode="cursor"
        hasMore={!!nextCursor}
        isLoadingMore={isFetching}
        onLoadMore={() => setCursor(nextCursor)}
        loadMoreLabel={t("common.loadMore")}
      />
    </div>
  )
}
