import { useParams } from "@tanstack/react-router"
import {
  IconAlertCircle,
  IconCalendarDue,
  IconClockExclamation,
  IconRefresh,
} from "@tabler/icons-react"
import { useWatchlist } from "@/hooks/use-watchlist"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { WatchlistSection } from "./watchlist-section"

export function WatchlistPage() {
  const { projectId } = useParams({ from: "/studio/$projectId/watchlist" })
  const { data, isLoading, error, refetch } = useWatchlist(projectId)

  const urgentTotal = (data?.trials_ending_soon?.length ?? 0) + (data?.past_due?.length ?? 0)

  return (
    <div className="flex flex-col min-h-full">
      <header className="flex items-center justify-between px-6 py-4 border-b">
        <div className="flex items-center gap-3">
          <IconAlertCircle className="size-4 text-muted-foreground" />
          <div className="flex flex-col gap-0.5">
            <h1 className="font-semibold text-sm">
              Watchlist
              {urgentTotal > 0 && (
                <span className="ml-1.5 inline-flex items-center px-1.5 py-0.5 rounded-full text-xs font-medium bg-red-500/10 text-red-700 dark:text-red-400">
                  {urgentTotal} urgent
                </span>
              )}
            </h1>
            <p className="text-xs text-muted-foreground">
              Subscriptions that need attention
            </p>
          </div>
        </div>
        <Button variant="outline" size="sm" className="gap-1.5" onClick={() => refetch()}>
          <IconRefresh className="size-4" />
          Refresh
        </Button>
      </header>

      <div className="p-6 space-y-8">
        {isLoading ? (
          <div className="space-y-6">
            {Array.from({ length: 4 }).map((_, i) => (
              <div key={i} className="space-y-3">
                <Skeleton className="h-5 w-48" />
                <div className="rounded-lg border divide-y overflow-hidden">
                  {Array.from({ length: 2 }).map((_, j) => (
                    <div key={j} className="px-4 py-3">
                      <Skeleton className="h-4 w-full" />
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        ) : error ? (
          <div className="flex flex-col items-center gap-2 py-16 text-muted-foreground text-sm">
            <span>Failed to load watchlist</span>
            <Button variant="outline" size="sm" onClick={() => refetch()}>Try again</Button>
          </div>
        ) : (
          <>
            <WatchlistSection
              title="Trials ending soon"
              subtitle="Trialing organizations with ≤ 7 days remaining"
              icon={<IconClockExclamation className="size-4" />}
              items={data?.trials_ending_soon ?? []}
              sectionType="trial"
              dateLabel="Trial ends"
              dateKey="trial_end"
              urgent
            />

            <WatchlistSection
              title="Past due"
              subtitle="Subscriptions that missed their renewal payment"
              icon={<IconAlertCircle className="size-4" />}
              items={data?.past_due ?? []}
              sectionType="past_due"
              dateLabel="Period ended"
              dateKey="period_end"
              urgent
            />

            <WatchlistSection
              title="Renewals due"
              subtitle="Active subscriptions renewing within 7 days"
              icon={<IconCalendarDue className="size-4" />}
              items={data?.renewals_due ?? []}
              sectionType="renewal"
              dateLabel="Renews on"
              dateKey="period_end"
            />

            <WatchlistSection
              title="Cancellations taking effect"
              subtitle="Cancelled subscriptions still within their paid period"
              icon={<IconCalendarDue className="size-4" />}
              items={data?.cancellations_pending ?? []}
              sectionType="cancellation"
              dateLabel="Access ends"
              dateKey="period_end"
            />
          </>
        )}
      </div>
    </div>
  )
}
