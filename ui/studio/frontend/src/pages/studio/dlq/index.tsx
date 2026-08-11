import { useState } from "react"
import { useParams } from "@tanstack/react-router"
import { IconMailExclamation, IconRefresh } from "@tabler/icons-react"
import type { DLQInfo } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { useQueues } from "@/hooks/use-dlq"
import { Button } from "@/components/ui/button"
import { isDLQ } from "./is-dlq"
import { MessagePanel } from "./message-panel"
import { PurgeDialog } from "./purge-dialog"
import { ViewToggle, type ViewMode } from "./view-toggle"
import { QueueTable, QueueTableSkeleton } from "./queue-table"

export function DLQPage() {
  const { projectId } = useParams({ from: "/studio/$projectId" })
  const {
    data: queues,
    isLoading,
    error,
    refetch,
    isFetching,
  } = useQueues(projectId)

  const [viewQueue, setViewQueue] = useState<DLQInfo | null>(null)
  const [purgeQueue, setPurgeQueue] = useState<DLQInfo | null>(null)
  const [viewMode, setViewMode] = useState<ViewMode>("all")

  const displayed =
    viewMode === "dlq"
      ? (queues ?? []).filter((q) => isDLQ(q.name))
      : (queues ?? [])

  const dlqCount = (queues ?? []).filter((q) => isDLQ(q.name)).length

  return (
    <div className="flex min-h-full flex-col">
      {/* Header */}
      <header className="flex items-center justify-between gap-4 border-b px-6 py-4">
        <div className="flex flex-col gap-0.5">
          <h1 className="text-sm font-semibold">Queue Monitor</h1>
          <p className="text-xs text-muted-foreground">
            {queues
              ? `${queues.length} queue${queues.length !== 1 ? "s" : ""} · ${dlqCount} dead letter`
              : "Loading…"}
          </p>
        </div>
        <div className="flex items-center gap-3">
          <ViewToggle value={viewMode} onChange={setViewMode} />
          <Button
            variant="outline"
            size="sm"
            onClick={() => refetch()}
            disabled={isFetching}
          >
            <IconRefresh
              className={`size-4 ${isFetching ? "animate-spin" : ""}`}
            />
            Refresh
          </Button>
        </div>
      </header>

      {/* Body */}
      <div className="flex-1 overflow-y-auto">
        {isLoading ? (
          <QueueTableSkeleton />
        ) : error ? (
          <div className="flex flex-col items-center justify-center gap-3 px-6 py-16 text-center">
            <p className="text-sm font-medium text-destructive">
              Failed to load queues
            </p>
            <p className="max-w-sm text-xs text-muted-foreground">
              {String(error)}
            </p>
            <p className="text-xs text-muted-foreground">
              Make sure the RabbitMQ Management plugin is enabled (port 15672).
            </p>
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              Try again
            </Button>
          </div>
        ) : displayed.length === 0 ? (
          <div className="flex flex-col items-center justify-center gap-3 py-16 text-center">
            <IconMailExclamation className="size-8 text-muted-foreground opacity-40" />
            <p className="text-sm text-muted-foreground">
              {viewMode === "dlq"
                ? "No dead letter queues found"
                : "No queues found"}
            </p>
          </div>
        ) : (
          <QueueTable
            queues={displayed}
            onView={setViewQueue}
            onPurge={setPurgeQueue}
          />
        )}
      </div>

      {viewQueue && (
        <MessagePanel
          projectId={projectId}
          queue={viewQueue}
          onClose={() => setViewQueue(null)}
        />
      )}

      {purgeQueue && (
        <PurgeDialog
          projectId={projectId}
          queue={purgeQueue}
          onClose={() => setPurgeQueue(null)}
        />
      )}
    </div>
  )
}
