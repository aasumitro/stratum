import { useParams } from "@tanstack/react-router"
import { IconRefresh } from "@tabler/icons-react"
import { useProjectMetrics } from "@/hooks/use-dashboard"
import { Button } from "@/components/ui/button"
import { DashboardSkeleton } from "./dashboard-skeleton"
import { DashboardContent } from "./dashboard-content"

export function DashboardPage() {
  const { projectId } = useParams({ from: "/studio/$projectId" })
  const {
    data: metrics,
    isLoading,
    error,
    refetch,
    isFetching,
  } = useProjectMetrics(projectId)

  return (
    <div className="flex min-h-full flex-col">
      {/* Page header */}
      <header className="flex items-center justify-between border-b px-6 py-4">
        <div className="flex flex-col gap-0.5">
          <h1 className="text-sm font-semibold">Dashboard</h1>
          <p className="text-xs text-muted-foreground">
            Live metrics from the project database
          </p>
        </div>
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
      </header>

      {isLoading ? (
        <DashboardSkeleton />
      ) : error ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center">
          <p className="text-sm font-medium text-destructive">
            Failed to load metrics
          </p>
          <p className="max-w-sm text-xs text-muted-foreground">
            {String(error)}
          </p>
          <Button variant="outline" size="sm" onClick={() => refetch()}>
            Try again
          </Button>
        </div>
      ) : metrics ? (
        <DashboardContent metrics={metrics} />
      ) : null}
    </div>
  )
}
